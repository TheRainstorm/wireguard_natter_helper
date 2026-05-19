package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/yfy/wireguard-natter-helper/internal/agent"
	"github.com/yfy/wireguard-natter-helper/internal/daemon"
	"github.com/yfy/wireguard-natter-helper/internal/rpc"
	"github.com/yfy/wireguard-natter-helper/internal/store"
	"github.com/yfy/wireguard-natter-helper/internal/webui"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "daemon":
		daemonCmd(os.Args[2:])
	case "agent":
		agentCmd(os.Args[2:])
	case "web":
		webCmd(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
}

func daemonCmd(args []string) {
	if len(args) < 1 {
		daemonUsage()
		os.Exit(2)
	}
	switch args[0] {
	case "init":
		fs := flag.NewFlagSet("wgnh daemon init", flag.ExitOnError)
		state := fs.String("state", "wgnh-state.json", "state file")
		_ = fs.Parse(args[1:])
		st, err := store.Open(*state)
		must(err)
		must(st.Save())
		fmt.Printf("initialized %s\n", *state)
	case "create-node":
		fs := flag.NewFlagSet("wgnh daemon create-node", flag.ExitOnError)
		state := fs.String("state", "wgnh-state.json", "state file")
		id := fs.String("id", "", "node id")
		name := fs.String("name", "", "node name")
		role := fs.String("role", "", "server or client")
		_ = fs.Parse(args[1:])
		if *id == "" || *role == "" {
			log.Fatal("--id and --role are required")
		}
		token, err := daemon.CreateNode(*state, *id, *name, *role)
		must(err)
		fmt.Printf("node_id=%s\n", *id)
		fmt.Printf("token=%s\n", token)
	case "add-binding":
		fs := flag.NewFlagSet("wgnh daemon add-binding", flag.ExitOnError)
		state := fs.String("state", "wgnh-state.json", "state file")
		b := store.Binding{}
		fs.StringVar(&b.ID, "id", "", "binding id")
		fs.StringVar(&b.ServerNodeID, "server-node", "", "server node id")
		fs.StringVar(&b.ServerInterface, "server-interface", "", "server WireGuard interface")
		fs.StringVar(&b.ClientNodeID, "client-node", "", "client node id")
		fs.StringVar(&b.ClientInterface, "client-interface", "", "client WireGuard interface")
		fs.StringVar(&b.PeerPublicKey, "peer-public-key", "", "peer public key to update on client")
		fs.StringVar(&b.ConfigType, "config-type", "openwrt_uci", "openwrt_uci, wg_conf, or runtime")
		fs.StringVar(&b.ConfigPath, "config-path", "", "config path for wg_conf")
		fs.StringVar(&b.ReloadMethod, "reload-method", "none", "none, ifup, wg-quick-restart, network-reload")
		_ = fs.Parse(args[1:])
		if b.ID == "" || b.ServerNodeID == "" || b.ClientNodeID == "" {
			log.Fatal("--id, --server-node, and --client-node are required")
		}
		st, err := store.Open(*state)
		must(err)
		must(st.AddBinding(b))
		fmt.Printf("binding %s saved\n", b.ID)
	case "serve":
		fs := flag.NewFlagSet("wgnh daemon serve", flag.ExitOnError)
		state := fs.String("state", "wgnh-state.json", "state file")
		addr := fs.String("addr", "127.0.0.1:8080", "listen address")
		adminToken := fs.String("admin-token", "", "optional admin token required for TCP admin requests")
		_ = fs.Parse(args[1:])
		st, err := store.Open(*state)
		must(err)
		fmt.Printf("wgnh daemon listening on tcp://%s\n", *addr)
		must(daemon.New(st, *adminToken).ListenAndServe(*addr))
	case "run-natter":
		fs := flag.NewFlagSet("wgnh daemon run-natter", flag.ExitOnError)
		addr := fs.String("addr", "127.0.0.1:8080", "daemon TCP address")
		adminToken := fs.String("admin-token", "", "admin token")
		serverNode := fs.String("server-node", "", "server node id")
		serverInterface := fs.String("server-interface", "", "server WireGuard interface")
		_ = fs.Parse(args[1:])
		if *serverNode == "" || *serverInterface == "" {
			log.Fatal("--server-node and --server-interface are required")
		}
		resp, err := rpc.Call(context.Background(), *addr, rpc.Request{
			Kind:            "admin.run_natter",
			AdminToken:      *adminToken,
			ServerNodeID:    *serverNode,
			ServerInterface: *serverInterface,
		}, 10*time.Second)
		must(err)
		fmt.Println(pretty(resp))
	case "nodes":
		adminList(*flagSetAddrToken("wgnh daemon nodes", args[1:]), "admin.nodes")
	case "bindings":
		adminList(*flagSetAddrToken("wgnh daemon bindings", args[1:]), "admin.bindings")
	case "events":
		fs := flag.NewFlagSet("wgnh daemon events", flag.ExitOnError)
		addr := fs.String("addr", "127.0.0.1:8080", "daemon TCP address")
		adminToken := fs.String("admin-token", "", "admin token")
		limit := fs.Int("limit", 100, "event limit")
		_ = fs.Parse(args[1:])
		resp, err := rpc.Call(context.Background(), *addr, rpc.Request{
			Kind:       "admin.events",
			AdminToken: *adminToken,
			Limit:      *limit,
		}, 10*time.Second)
		must(err)
		fmt.Println(pretty(resp))
	default:
		daemonUsage()
		os.Exit(2)
	}
}

type addrTokenFlags struct {
	Addr       string
	AdminToken string
}

func flagSetAddrToken(name string, args []string) *addrTokenFlags {
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	addr := fs.String("addr", "127.0.0.1:8080", "daemon TCP address")
	adminToken := fs.String("admin-token", "", "admin token")
	_ = fs.Parse(args)
	return &addrTokenFlags{Addr: *addr, AdminToken: *adminToken}
}

func adminList(flags addrTokenFlags, kind string) {
	resp, err := rpc.Call(context.Background(), flags.Addr, rpc.Request{
		Kind:       kind,
		AdminToken: flags.AdminToken,
	}, 10*time.Second)
	must(err)
	fmt.Println(pretty(resp))
}

func agentCmd(args []string) {
	fs := flag.NewFlagSet("wgnh agent", flag.ExitOnError)
	configPath := fs.String("config", "", "agent config json")
	_ = fs.Parse(args)
	if *configPath == "" {
		log.Fatal("--config is required")
	}
	cfg, err := agent.LoadConfig(*configPath)
	must(err)
	a, err := agent.New(cfg)
	must(err)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	err = a.Run(ctx)
	if err != nil && err != context.Canceled {
		log.Fatal(err)
	}
}

func webCmd(args []string) {
	fs := flag.NewFlagSet("wgnh web", flag.ExitOnError)
	addr := fs.String("addr", "127.0.0.1:9090", "local web listen address")
	daemonAddr := fs.String("daemon-addr", "127.0.0.1:3333", "daemon TCP address")
	adminToken := fs.String("admin-token", "", "admin token")
	_ = fs.Parse(args)
	must(webui.New(*addr, *daemonAddr, *adminToken).ListenAndServe())
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: wgnh <daemon|agent|web> ...")
}

func daemonUsage() {
	fmt.Fprintln(os.Stderr, "usage: wgnh daemon <init|create-node|add-binding|serve|run-natter|nodes|bindings|events> ...")
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

func pretty(v any) string {
	raw, _ := json.MarshalIndent(v, "", "  ")
	return string(raw)
}
