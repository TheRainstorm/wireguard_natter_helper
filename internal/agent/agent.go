package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/yfy/wireguard-natter-helper/internal/protocol"
	"github.com/yfy/wireguard-natter-helper/internal/rpc"
	"github.com/yfy/wireguard-natter-helper/internal/wgconfig"
)

type Config struct {
	NodeID       string        `json:"node_id"`
	DaemonAddr   string        `json:"daemon_addr"`
	DaemonURL    string        `json:"daemon_url"`
	Token        string        `json:"token"`
	TokenFile    string        `json:"token_file"`
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
	config Config
	token  string
	addr   string
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
	token := cfg.Token
	if token == "" && cfg.TokenFile != "" {
		raw, err := os.ReadFile(cfg.TokenFile)
		if err != nil {
			return nil, err
		}
		token = string(bytes.TrimSpace(raw))
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
		return nil, errors.New("node_id, daemon_addr and token/token_file are required")
	}
	return &Agent{config: cfg, token: token, addr: addr}, nil
}

func (a *Agent) Run(ctx context.Context) error {
	if a.config.Monitor.Enabled {
		go a.runMonitor(ctx)
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		cmd, err := a.poll(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "agent error: %v\n", err)
			time.Sleep(time.Duration(a.config.RetrySeconds) * time.Second)
			continue
		}
		if cmd != nil {
			a.handle(ctx, *cmd)
		}
	}
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
	log.Printf("monitor started peers=%d interval=%s stale=%s fail_threshold=%d", len(a.config.Monitor.Peers), interval, stale, failThreshold)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for _, peer := range a.config.Monitor.Peers {
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
						_ = a.report(ctx, "peer.unreachable", "", map[string]any{
							"binding_id":       peer.BindingID,
							"interface":        peer.Interface,
							"peer_public_key":  peer.PeerPublicKey,
							"server_node_id":   peer.ServerNodeID,
							"server_interface": peer.ServerInterface,
							"last_handshake":   status.LastHandshake,
							"age_seconds":      int(status.Age.Seconds()),
							"stale_seconds":    int(stale.Seconds()),
						})
					}
					continue
				}
				if unreachable[key] {
					_ = a.report(ctx, "peer.recovered", "", map[string]any{
						"binding_id":       peer.BindingID,
						"interface":        peer.Interface,
						"peer_public_key":  peer.PeerPublicKey,
						"server_node_id":   peer.ServerNodeID,
						"server_interface": peer.ServerInterface,
						"last_handshake":   status.LastHandshake,
					})
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
		Meta:   map[string]any{"platform": runtime.GOOS + "/" + runtime.GOARCH, "agent_version": protocol.Version},
	}, 40*time.Second)
	if err != nil {
		return nil, err
	}
	return resp.Command, nil
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
