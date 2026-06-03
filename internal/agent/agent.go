package agent

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yfy/wireguard-natter-helper/internal/protocol"
	"github.com/yfy/wireguard-natter-helper/internal/rpc"
	"github.com/yfy/wireguard-natter-helper/internal/store"
	"github.com/yfy/wireguard-natter-helper/internal/wgconfig"
)

type Config struct {
	NodeID       string        `json:"node_id"`
	NodeName     string        `json:"node_name"`
	DaemonAddr   string        `json:"daemon_addr"`
	DaemonURL    string        `json:"daemon_url"`
	Token        string        `json:"token"`
	TokenFile    string        `json:"token_file"`
	JoinCode     string        `json:"join_code"`
	StatePath    string        `json:"state_path"`
	Role         string        `json:"role"`
	RetrySeconds int           `json:"retry_seconds"`
	DryRun       bool          `json:"dry_run"`
	WireGuard    []WGInterface `json:"wireguard"`
	Natter       NatterConfig  `json:"natter"`
	Monitor      MonitorConfig `json:"monitor"`
}

type WGInterface struct {
	Name            string `json:"name"`
	ListenPort      int    `json:"listen_port"`
	ConfigType      string `json:"config_type"`
	ConfigPath      string `json:"config_path"`
	WGControlMethod string `json:"wireguard_control_method"`
}

type NatterConfig struct {
	Command                []string `json:"command"`
	TimeoutSeconds         int      `json:"timeout_seconds"`
	StopWireGuard          bool     `json:"stop_wireguard"`
	WireGuardControlMethod string   `json:"wireguard_control_method"`
	RestartDelaySeconds    int      `json:"restart_delay_seconds"`
}

type MonitorConfig struct {
	Enabled         bool          `json:"enabled"`
	IntervalSeconds int           `json:"interval_seconds"`
	StaleSeconds    int           `json:"stale_seconds"`
	FailThreshold   int           `json:"fail_threshold"`
	Peers           []MonitorPeer `json:"peers"`
}

type MonitorPeer struct {
	BindingID       string `json:"binding_id"`
	Interface       string `json:"interface"`
	PeerPublicKey   string `json:"peer_public_key"`
	ServerNodeID    string `json:"server_node_id"`
	ServerInterface string `json:"server_interface"`
}

type Agent struct {
	config       Config
	token        string
	addr         string
	joined       bool
	monitorOnce  sync.Once
	monitorMu    sync.RWMutex
	monitorPeers []MonitorPeer
}

type LocalState struct {
	NodeID string `json:"node_id"`
	Token  string `json:"token"`
}

func LoadConfig(path string) (Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return Config{}, err
	}
	if cfg.RetrySeconds == 0 {
		cfg.RetrySeconds = 5
	}
	return cfg, nil
}

func New(cfg Config) (*Agent, error) {
	if cfg.StatePath == "" {
		cfg.StatePath = "/etc/wgnh/node-state.json"
	}
	token := cfg.Token
	if token == "" && cfg.TokenFile != "" {
		raw, err := os.ReadFile(cfg.TokenFile)
		if err != nil {
			return nil, err
		}
		token = string(bytes.TrimSpace(raw))
	}
	if cfg.NodeID == "" || token == "" {
		state, err := loadOrCreateLocalState(cfg.StatePath)
		if err != nil {
			return nil, err
		}
		if cfg.NodeID == "" {
			cfg.NodeID = state.NodeID
		}
		if token == "" {
			token = state.Token
		}
	}
	addr := cfg.DaemonAddr
	if addr == "" {
		addr = cfg.DaemonURL
	}
	addr, err := rpc.NormalizeAddr(addr)
	if err != nil {
		return nil, err
	}
	if cfg.NodeID == "" || addr == "" || token == "" {
		return nil, errors.New("daemon_addr is required; node_id and token are auto-created in state_path when omitted")
	}
	return &Agent{config: cfg, token: token, addr: addr, monitorPeers: cfg.Monitor.Peers}, nil
}

func (a *Agent) Run(ctx context.Context) error {
	a.startMonitorIfNeeded(ctx)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if !a.joined {
			if joinErr := a.enroll(ctx); joinErr != nil {
				if strings.Contains(joinErr.Error(), "node pending approval") {
					fmt.Fprintf(os.Stderr, "agent pending approval: node_id=%s\n", a.config.NodeID)
				} else {
					fmt.Fprintf(os.Stderr, "agent join error: %v\n", joinErr)
				}
				time.Sleep(time.Duration(a.config.RetrySeconds) * time.Second)
				continue
			}
			log.Printf("agent joined and approved node_id=%s", a.config.NodeID)
		}
		cmd, err := a.poll(ctx)
		if err != nil {
			if strings.Contains(err.Error(), "node pending approval") {
				if joinErr := a.enroll(ctx); joinErr != nil {
					fmt.Fprintf(os.Stderr, "agent join error: %v\n", joinErr)
				} else {
					fmt.Fprintf(os.Stderr, "agent pending approval: node_id=%s\n", a.config.NodeID)
				}
				time.Sleep(time.Duration(a.config.RetrySeconds) * time.Second)
				continue
			}
			if !a.joined {
				if joinErr := a.enroll(ctx); joinErr == nil {
					time.Sleep(time.Duration(a.config.RetrySeconds) * time.Second)
					continue
				}
			}
			fmt.Fprintf(os.Stderr, "agent error: %v\n", err)
			time.Sleep(time.Duration(a.config.RetrySeconds) * time.Second)
			continue
		}
		if cmd != nil {
			a.handle(ctx, *cmd)
		}
	}
}

func (a *Agent) enroll(ctx context.Context) error {
	if a.config.JoinCode != "" {
		return a.join(ctx)
	}
	return a.register(ctx)
}

func loadOrCreateLocalState(path string) (LocalState, error) {
	raw, err := os.ReadFile(path)
	if err == nil && len(bytes.TrimSpace(raw)) > 0 {
		var state LocalState
		if err := json.Unmarshal(raw, &state); err != nil {
			return LocalState{}, err
		}
		if state.NodeID != "" && state.Token != "" {
			return state, nil
		}
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return LocalState{}, err
	}
	state := LocalState{NodeID: "node-" + randomHex(8), Token: randomHex(32)}
	if err := os.MkdirAll(filepathDir(path), 0o755); err != nil {
		return LocalState{}, err
	}
	raw, _ = json.MarshalIndent(state, "", "  ")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return LocalState{}, err
	}
	return state, nil
}

func filepathDir(path string) string {
	idx := strings.LastIndex(path, "/")
	if idx <= 0 {
		return "."
	}
	return path[:idx]
}

func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	return hex.EncodeToString(buf)
}

func (a *Agent) join(ctx context.Context) error {
	resp, err := rpc.Call(ctx, a.addr, rpc.Request{
		Kind:     "agent.join",
		NodeID:   a.config.NodeID,
		Name:     a.config.NodeName,
		Token:    a.token,
		JoinCode: a.config.JoinCode,
		Meta:     a.meta(),
	}, 10*time.Second)
	if err != nil {
		return err
	}
	if !resp.Approved {
		return errors.New("node pending approval")
	}
	a.joined = true
	return nil
}

func (a *Agent) register(ctx context.Context) error {
	resp, err := rpc.Call(ctx, a.addr, rpc.Request{
		Kind:   "agent.register",
		NodeID: a.config.NodeID,
		Name:   a.config.NodeName,
		Token:  a.token,
		Meta:   a.meta(),
	}, 10*time.Second)
	if err != nil {
		return err
	}
	if !resp.Approved {
		return errors.New("node pending approval")
	}
	a.joined = true
	return nil
}

func (a *Agent) runMonitor(ctx context.Context) {
	interval := time.Duration(a.config.Monitor.IntervalSeconds) * time.Second
	if interval <= 0 {
		interval = 30 * time.Second
	}
	stale := time.Duration(a.config.Monitor.StaleSeconds) * time.Second
	if stale <= 0 {
		stale = 180 * time.Second
	}
	failThreshold := a.config.Monitor.FailThreshold
	if failThreshold <= 0 {
		failThreshold = 3
	}
	failCounts := map[string]int{}
	unreachable := map[string]bool{}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	log.Printf("monitor started interval=%s stale=%s fail_threshold=%d", interval, stale, failThreshold)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			peers := a.currentMonitorPeers()
			if len(peers) == 0 {
				log.Printf("monitor has no peers yet; waiting for daemon binding sync")
				continue
			}
			for _, peer := range peers {
				key := peer.BindingID
				if key == "" {
					key = peer.Interface + "/" + peer.PeerPublicKey
				}
				status, err := probePeer(peer, stale)
				if err != nil {
					log.Printf("monitor probe failed binding=%s interface=%s error=%v", peer.BindingID, peer.Interface, err)
					continue
				}
				if status.Stale {
					failCounts[key]++
					log.Printf("monitor stale binding=%s interface=%s peer=%s age=%s failures=%d/%d", peer.BindingID, peer.Interface, shortKey(peer.PeerPublicKey), status.Age, failCounts[key], failThreshold)
					if failCounts[key] >= failThreshold && !unreachable[key] {
						unreachable[key] = true
						if err := a.report(ctx, "peer.unreachable", "", map[string]any{
							"binding_id":       peer.BindingID,
							"interface":        peer.Interface,
							"peer_public_key":  peer.PeerPublicKey,
							"server_node_id":   peer.ServerNodeID,
							"server_interface": peer.ServerInterface,
							"last_handshake":   status.LastHandshake,
							"age_seconds":      int(status.Age.Seconds()),
							"stale_seconds":    int(stale.Seconds()),
						}); err != nil {
							log.Printf("monitor report peer.unreachable failed binding=%s error=%v", peer.BindingID, err)
						}
					}
					continue
				}
				if unreachable[key] {
					if err := a.report(ctx, "peer.recovered", "", map[string]any{
						"binding_id":       peer.BindingID,
						"interface":        peer.Interface,
						"peer_public_key":  peer.PeerPublicKey,
						"server_node_id":   peer.ServerNodeID,
						"server_interface": peer.ServerInterface,
						"last_handshake":   status.LastHandshake,
					}); err != nil {
						log.Printf("monitor report peer.recovered failed binding=%s error=%v", peer.BindingID, err)
					}
					log.Printf("monitor recovered binding=%s interface=%s peer=%s", peer.BindingID, peer.Interface, shortKey(peer.PeerPublicKey))
				}
				failCounts[key] = 0
				unreachable[key] = false
			}
		}
	}
}

func (a *Agent) poll(ctx context.Context) (*protocol.Command, error) {
	resp, err := rpc.Call(ctx, a.addr, rpc.Request{
		Kind:   "agent.poll",
		NodeID: a.config.NodeID,
		Token:  a.token,
		Meta:   a.meta(),
	}, 40*time.Second)
	if err != nil {
		return nil, err
	}
	if len(resp.Nodes) > 0 {
		a.applyRemoteNodeConfig(resp.Nodes[0])
	}
	if len(resp.MonitorPeers) > 0 {
		a.setMonitorPeers(resp.MonitorPeers)
		a.startMonitorIfNeeded(ctx)
	}
	return resp.Command, nil
}

func (a *Agent) applyRemoteNodeConfig(node store.Node) {
	changed := false
	if node.Role != "" && a.config.Role != node.Role {
		a.config.Role = node.Role
		changed = true
	}
	if node.Interface != "" {
		item := a.ensureWireGuardInterface(node.Interface)
		if node.ConfigType != "" && item.ConfigType != node.ConfigType {
			item.ConfigType = node.ConfigType
			changed = true
		}
		if node.ReloadMethod != "" && item.WGControlMethod == "" {
			switch node.ReloadMethod {
			case "ifup":
				item.WGControlMethod = "ifup"
				changed = true
			case "wg-quick-restart":
				item.WGControlMethod = "wg-quick"
				changed = true
			}
		}
		a.setWireGuardInterface(item)
	}
	if node.Role == "client" && !a.config.Monitor.Enabled {
		a.config.Monitor.Enabled = true
		changed = true
	}
	if node.NatterConfigured {
		if !sameStrings(a.config.Natter.Command, node.NatterCommand) {
			a.config.Natter.Command = append([]string(nil), node.NatterCommand...)
			changed = true
		}
		if node.NatterTimeoutSeconds > 0 && a.config.Natter.TimeoutSeconds != node.NatterTimeoutSeconds {
			a.config.Natter.TimeoutSeconds = node.NatterTimeoutSeconds
			changed = true
		}
		if node.NatterStopWireGuard != a.config.Natter.StopWireGuard {
			a.config.Natter.StopWireGuard = node.NatterStopWireGuard
			changed = true
		}
		if node.NatterWireGuardControl != "" && a.config.Natter.WireGuardControlMethod != node.NatterWireGuardControl {
			a.config.Natter.WireGuardControlMethod = node.NatterWireGuardControl
			changed = true
		}
		if node.NatterRestartDelaySeconds > 0 && a.config.Natter.RestartDelaySeconds != node.NatterRestartDelaySeconds {
			a.config.Natter.RestartDelaySeconds = node.NatterRestartDelaySeconds
			changed = true
		}
	} else if node.NatterManaged && len(a.config.Natter.Command) > 0 {
		a.config.Natter = NatterConfig{}
		changed = true
	}
	if changed {
		log.Printf("agent applied remote config role=%s interface=%s config_type=%s reload_method=%s", node.Role, node.Interface, node.ConfigType, node.ReloadMethod)
	}
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (a *Agent) ensureWireGuardInterface(name string) WGInterface {
	for _, item := range a.config.WireGuard {
		if item.Name == name {
			return item
		}
	}
	return WGInterface{Name: name}
}

func (a *Agent) setWireGuardInterface(next WGInterface) {
	for i, item := range a.config.WireGuard {
		if item.Name == next.Name {
			a.config.WireGuard[i] = next
			return
		}
	}
	a.config.WireGuard = append(a.config.WireGuard, next)
}

func (a *Agent) startMonitorIfNeeded(ctx context.Context) {
	if !a.config.Monitor.Enabled {
		return
	}
	a.monitorOnce.Do(func() {
		go a.runMonitor(ctx)
	})
}

func (a *Agent) meta() map[string]any {
	meta := map[string]any{"platform": runtime.GOOS + "/" + runtime.GOARCH, "agent_version": protocol.Version}
	if inventory := a.wireGuardInventory(); len(inventory) > 0 {
		meta["wireguard"] = inventory
	}
	return meta
}

func (a *Agent) wireGuardInventory() []map[string]any {
	configured := map[string]WGInterface{}
	var names []string
	for _, item := range a.config.WireGuard {
		if item.Name == "" {
			continue
		}
		configured[item.Name] = item
		names = append(names, item.Name)
	}
	if len(names) == 0 {
		discovered, err := wgInterfaces()
		if err != nil {
			return nil
		}
		names = discovered
	}
	var out []map[string]any
	for _, name := range names {
		item := configured[name]
		publicKey, err := wgShowString(name, "public-key")
		if err != nil || publicKey == "" {
			continue
		}
		listenPort := item.ListenPort
		if listenPort <= 0 {
			listenPort, _ = wgShowInt(name, "listen-port")
		}
		peers, _ := wgPeers(name)
		payload := map[string]any{
			"name":       name,
			"public_key": publicKey,
			"peers":      peers,
		}
		if listenPort > 0 {
			payload["listen_port"] = listenPort
		}
		if item.ConfigType != "" {
			payload["config_type"] = item.ConfigType
		}
		if item.ConfigPath != "" {
			payload["config_path"] = item.ConfigPath
		}
		out = append(out, payload)
	}
	return out
}

func (a *Agent) setMonitorPeers(peers []rpc.MonitorPeer) {
	next := make([]MonitorPeer, 0, len(peers))
	for _, peer := range peers {
		next = append(next, MonitorPeer{
			BindingID:       peer.BindingID,
			Interface:       peer.Interface,
			PeerPublicKey:   peer.PeerPublicKey,
			ServerNodeID:    peer.ServerNodeID,
			ServerInterface: peer.ServerInterface,
		})
	}
	a.monitorMu.Lock()
	a.monitorPeers = next
	a.monitorMu.Unlock()
}

func (a *Agent) currentMonitorPeers() []MonitorPeer {
	a.monitorMu.RLock()
	defer a.monitorMu.RUnlock()
	peers := make([]MonitorPeer, len(a.monitorPeers))
	copy(peers, a.monitorPeers)
	return peers
}

func (a *Agent) handle(ctx context.Context, cmd protocol.Command) {
	log.Printf("received command id=%s action=%s", cmd.CommandID, cmd.Action)
	switch cmd.Action {
	case "natter.run":
		result, err := a.runNatter(ctx, cmd.Payload)
		if err != nil {
			log.Printf("command id=%s action=%s failed: %v", cmd.CommandID, cmd.Action, err)
			a.report(ctx, "action.result", cmd.CommandID, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		_ = a.report(ctx, "natter.result", "", result)
		_ = a.report(ctx, "action.result", cmd.CommandID, map[string]any{"ok": true, "detail": "natter completed"})
		log.Printf("command id=%s action=%s completed public=%s:%d", cmd.CommandID, cmd.Action, stringField(result, "public_ip"), intField(result, "public_port"))
	case "endpoint.apply":
		result, err := a.applyEndpoint(cmd.Payload)
		if err != nil {
			log.Printf("command id=%s action=%s failed: %v", cmd.CommandID, cmd.Action, err)
			a.report(ctx, "action.result", cmd.CommandID, map[string]any{"ok": false, "binding_id": stringField(cmd.Payload, "binding_id"), "error": err.Error()})
			return
		}
		_ = a.report(ctx, "action.result", cmd.CommandID, map[string]any{
			"ok": true, "binding_id": stringField(cmd.Payload, "binding_id"), "changed": result.Changed, "message": result.Message,
		})
		log.Printf(
			"command id=%s action=%s completed binding=%s interface=%s endpoint=%s:%d changed=%t message=%q",
			cmd.CommandID,
			cmd.Action,
			stringField(cmd.Payload, "binding_id"),
			stringField(cmd.Payload, "interface"),
			stringField(cmd.Payload, "endpoint_host"),
			intField(cmd.Payload, "endpoint_port"),
			result.Changed,
			result.Message,
		)
	default:
		log.Printf("command id=%s action=%s unsupported", cmd.CommandID, cmd.Action)
		_ = a.report(ctx, "action.result", cmd.CommandID, map[string]any{"ok": false, "error": "unsupported action: " + cmd.Action})
	}
}

func (a *Agent) runNatter(ctx context.Context, payload map[string]any) (map[string]any, error) {
	if len(a.config.Natter.Command) == 0 {
		return nil, errors.New("natter.command is not configured")
	}
	serverInterface := stringField(payload, "server_interface")
	if serverInterface == "" {
		return nil, errors.New("server_interface is required")
	}

	stoppedWireGuard := false
	controlMethod := ""
	if a.config.Natter.StopWireGuard {
		method, err := a.wireGuardControlMethod(serverInterface)
		if err != nil {
			return nil, err
		}
		log.Printf("stopping WireGuard interface=%s method=%s before natter", serverInterface, method)
		if err := wgconfig.StopInterface(serverInterface, method); err != nil {
			return nil, fmt.Errorf("stop WireGuard interface %s failed: %w", serverInterface, err)
		}
		log.Printf("stopped WireGuard interface=%s", serverInterface)
		stoppedWireGuard = true
		controlMethod = method
	}

	timeout := time.Duration(a.config.Natter.TimeoutSeconds) * time.Second
	if timeout == 0 {
		timeout = 90 * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	log.Printf("running natter command for interface=%s timeout=%s", serverInterface, timeout)
	cmd := exec.CommandContext(runCtx, a.config.Natter.Command[0], a.config.Natter.Command[1:]...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if startErr := a.restartWireGuardIfNeeded(serverInterface, controlMethod, stoppedWireGuard); startErr != nil {
			return nil, fmt.Errorf("natter command failed: %w: %s; additionally %v", err, string(out), startErr)
		}
		return nil, fmt.Errorf("natter command failed: %w: %s", err, string(out))
	}
	result, err := ParseNatterOutput(out)
	if err != nil {
		if startErr := a.restartWireGuardIfNeeded(serverInterface, controlMethod, stoppedWireGuard); startErr != nil {
			return nil, fmt.Errorf("%w; additionally %v", err, startErr)
		}
		return nil, err
	}
	if startErr := a.restartWireGuardIfNeeded(serverInterface, controlMethod, stoppedWireGuard); startErr != nil {
		return nil, startErr
	}
	result["server_interface"] = serverInterface
	log.Printf("natter result interface=%s public=%s:%d local=%s:%d", serverInterface, stringField(result, "public_ip"), intField(result, "public_port"), stringField(result, "local_ip"), intField(result, "local_port"))
	return result, nil
}

func (a *Agent) restartWireGuardIfNeeded(interfaceName, method string, stopped bool) error {
	if !stopped {
		return nil
	}
	if delay := a.config.Natter.RestartDelaySeconds; delay > 0 {
		log.Printf("waiting %d seconds before restarting WireGuard interface=%s", delay, interfaceName)
		time.Sleep(time.Duration(delay) * time.Second)
	}
	log.Printf("starting WireGuard interface=%s method=%s after natter", interfaceName, method)
	if err := wgconfig.StartInterface(interfaceName, method); err != nil {
		return fmt.Errorf("start WireGuard interface %s failed: %w", interfaceName, err)
	}
	log.Printf("started WireGuard interface=%s", interfaceName)
	return nil
}

func (a *Agent) wireGuardControlMethod(interfaceName string) (string, error) {
	if a.config.Natter.WireGuardControlMethod != "" {
		return a.config.Natter.WireGuardControlMethod, nil
	}
	for _, item := range a.config.WireGuard {
		if item.Name == interfaceName {
			if item.WGControlMethod != "" {
				return item.WGControlMethod, nil
			}
			switch item.ConfigType {
			case "openwrt_uci":
				return "ifup", nil
			case "wg_conf", "runtime":
				return "wg-quick", nil
			default:
				return "", fmt.Errorf("wireguard_control_method is required for interface %s", interfaceName)
			}
		}
	}
	return "", fmt.Errorf("wireguard interface %s is not configured", interfaceName)
}

func ParseNatterOutput(out []byte) (map[string]any, error) {
	lines := bytes.Split(out, []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		line := bytes.TrimSpace(lines[i])
		if len(line) == 0 {
			continue
		}
		var payload map[string]any
		dec := json.NewDecoder(bytes.NewReader(line))
		dec.UseNumber()
		if err := dec.Decode(&payload); err == nil {
			return payload, nil
		}
	}
	return nil, errors.New("natter command must print a JSON result line")
}

func (a *Agent) applyEndpoint(payload map[string]any) (wgconfig.ApplyResult, error) {
	return wgconfig.ApplyEndpoint(wgconfig.ApplyRequest{
		ConfigType:    stringField(payload, "config_type"),
		Interface:     stringField(payload, "interface"),
		PeerPublicKey: stringField(payload, "peer_public_key"),
		EndpointHost:  stringField(payload, "endpoint_host"),
		EndpointPort:  intField(payload, "endpoint_port"),
		ConfigPath:    stringField(payload, "config_path"),
		ReloadMethod:  stringField(payload, "reload_method"),
		DryRun:        a.config.DryRun,
	})
}

func (a *Agent) report(ctx context.Context, typ, commandID string, payload map[string]any) error {
	_, err := rpc.Call(ctx, a.addr, rpc.Request{
		Kind:       "agent.report",
		NodeID:     a.config.NodeID,
		Token:      a.token,
		ReportType: typ,
		CommandID:  commandID,
		Payload:    payload,
	}, 10*time.Second)
	return err
}

func stringField(payload map[string]any, key string) string {
	v, _ := payload[key].(string)
	return v
}

func intField(payload map[string]any, key string) int {
	switch v := payload[key].(type) {
	case int:
		return v
	case float64:
		return int(v)
	case json.Number:
		n, _ := v.Int64()
		return int(n)
	default:
		return 0
	}
}

type peerProbeStatus struct {
	LastHandshake int64
	Age           time.Duration
	Stale         bool
}

func probePeer(peer MonitorPeer, staleAfter time.Duration) (peerProbeStatus, error) {
	handshakes, err := latestHandshakes(peer.Interface)
	if err != nil {
		return peerProbeStatus{}, err
	}
	ts, ok := handshakes[peer.PeerPublicKey]
	if !ok {
		return peerProbeStatus{LastHandshake: 0, Age: time.Duration(1<<63 - 1), Stale: true}, nil
	}
	if ts <= 0 {
		return peerProbeStatus{LastHandshake: 0, Age: time.Duration(1<<63 - 1), Stale: true}, nil
	}
	age := time.Since(time.Unix(ts, 0))
	return peerProbeStatus{LastHandshake: ts, Age: age, Stale: age > staleAfter}, nil
}

func latestHandshakes(interfaceName string) (map[string]int64, error) {
	out, err := exec.Command("wg", "show", interfaceName, "latest-handshakes").Output()
	if err != nil {
		return nil, fmt.Errorf("wg show %s latest-handshakes failed: %w", interfaceName, err)
	}
	return ParseLatestHandshakes(string(out))
}

func wgInterfaces() ([]string, error) {
	out, err := exec.Command("wg", "show", "interfaces").Output()
	if err != nil {
		return nil, fmt.Errorf("wg show interfaces failed: %w", err)
	}
	return parseWhitespaceList(string(out)), nil
}

func wgPeers(interfaceName string) ([]string, error) {
	out, err := exec.Command("wg", "show", interfaceName, "peers").Output()
	if err != nil {
		return nil, fmt.Errorf("wg show %s peers failed: %w", interfaceName, err)
	}
	return parseWhitespaceList(string(out)), nil
}

func wgShowString(interfaceName, field string) (string, error) {
	out, err := exec.Command("wg", "show", interfaceName, field).Output()
	if err != nil {
		return "", fmt.Errorf("wg show %s %s failed: %w", interfaceName, field, err)
	}
	return strings.TrimSpace(string(out)), nil
}

func wgShowInt(interfaceName, field string) (int, error) {
	value, err := wgShowString(interfaceName, field)
	if err != nil {
		return 0, err
	}
	if value == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("invalid wg show %s %s value %q: %w", interfaceName, field, value, err)
	}
	return n, nil
}

func parseWhitespaceList(raw string) []string {
	var out []string
	for _, item := range strings.Fields(raw) {
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}

func ParseLatestHandshakes(raw string) (map[string]int64, error) {
	result := map[string]int64{}
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return nil, fmt.Errorf("invalid latest-handshakes line: %q", line)
		}
		ts, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid latest-handshake timestamp for peer %s: %w", fields[0], err)
		}
		result[fields[0]] = ts
	}
	return result, nil
}

func shortKey(key string) string {
	if len(key) <= 12 {
		return key
	}
	return key[:6] + "..." + key[len(key)-6:]
}
