package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/yfy/wireguard-natter-helper/internal/protocol"
	"github.com/yfy/wireguard-natter-helper/internal/wgconfig"
)

type Config struct {
	NodeID       string        `json:"node_id"`
	DaemonURL    string        `json:"daemon_url"`
	Token        string        `json:"token"`
	TokenFile    string        `json:"token_file"`
	Role         string        `json:"role"`
	RetrySeconds int           `json:"retry_seconds"`
	DryRun       bool          `json:"dry_run"`
	WireGuard    []WGInterface `json:"wireguard"`
	Natter       NatterConfig  `json:"natter"`
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

type Agent struct {
	config Config
	token  string
	client *http.Client
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
	if cfg.NodeID == "" || cfg.DaemonURL == "" || token == "" {
		return nil, errors.New("node_id, daemon_url and token/token_file are required")
	}
	return &Agent{config: cfg, token: token, client: &http.Client{}}, nil
}

func (a *Agent) Run(ctx context.Context) error {
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

func (a *Agent) poll(ctx context.Context) (*protocol.Command, error) {
	body := map[string]any{"meta": map[string]any{"platform": runtime.GOOS + "/" + runtime.GOARCH, "agent_version": protocol.Version}}
	var resp struct {
		OK      bool              `json:"ok"`
		Command *protocol.Command `json:"command"`
	}
	if err := a.post(ctx, "/agent/poll", body, &resp); err != nil {
		return nil, err
	}
	return resp.Command, nil
}

func (a *Agent) handle(ctx context.Context, cmd protocol.Command) {
	switch cmd.Action {
	case "natter.run":
		result, err := a.runNatter(ctx, cmd.Payload)
		if err != nil {
			a.report(ctx, "action.result", cmd.CommandID, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		_ = a.report(ctx, "natter.result", "", result)
		_ = a.report(ctx, "action.result", cmd.CommandID, map[string]any{"ok": true, "detail": "natter completed"})
	case "endpoint.apply":
		result, err := a.applyEndpoint(cmd.Payload)
		if err != nil {
			a.report(ctx, "action.result", cmd.CommandID, map[string]any{"ok": false, "binding_id": stringField(cmd.Payload, "binding_id"), "error": err.Error()})
			return
		}
		_ = a.report(ctx, "action.result", cmd.CommandID, map[string]any{
			"ok": true, "binding_id": stringField(cmd.Payload, "binding_id"), "changed": result.Changed, "message": result.Message,
		})
	default:
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
		if err := wgconfig.StopInterface(serverInterface, method); err != nil {
			return nil, fmt.Errorf("stop WireGuard interface %s failed: %w", serverInterface, err)
		}
		stoppedWireGuard = true
		controlMethod = method
	}

	timeout := time.Duration(a.config.Natter.TimeoutSeconds) * time.Second
	if timeout == 0 {
		timeout = 90 * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
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
	return result, nil
}

func (a *Agent) restartWireGuardIfNeeded(interfaceName, method string, stopped bool) error {
	if !stopped {
		return nil
	}
	if delay := a.config.Natter.RestartDelaySeconds; delay > 0 {
		time.Sleep(time.Duration(delay) * time.Second)
	}
	if err := wgconfig.StartInterface(interfaceName, method); err != nil {
		return fmt.Errorf("start WireGuard interface %s failed: %w", interfaceName, err)
	}
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
	body := map[string]any{"type": typ, "payload": payload}
	if commandID != "" {
		body["command_id"] = commandID
	}
	var resp map[string]any
	return a.post(ctx, "/agent/report", body, &resp)
}

func (a *Agent) post(ctx context.Context, path string, body any, dst any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.config.DaemonURL+path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Node-ID", a.config.NodeID)
	req.Header.Set("Authorization", "Bearer "+a.token)
	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("daemon returned %s", resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(dst)
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
