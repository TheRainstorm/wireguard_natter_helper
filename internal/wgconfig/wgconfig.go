package wgconfig

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type ApplyRequest struct {
	ConfigType    string
	Interface     string
	PeerPublicKey string
	EndpointHost  string
	EndpointPort  int
	ConfigPath    string
	ReloadMethod  string
	DryRun        bool
}

type ApplyResult struct {
	Changed bool   `json:"changed"`
	Message string `json:"message"`
}

func ApplyEndpoint(req ApplyRequest) (ApplyResult, error) {
	endpoint := fmt.Sprintf("%s:%d", req.EndpointHost, req.EndpointPort)
	var result ApplyResult
	var err error
	switch req.ConfigType {
	case "wg_conf":
		path := req.ConfigPath
		if path == "" {
			path = filepath.Join("/etc/wireguard", req.Interface+".conf")
		}
		result, err = UpdateWGConf(path, req.PeerPublicKey, endpoint, req.DryRun)
	case "openwrt_uci":
		result, err = UpdateOpenWrtUCI(req.Interface, req.PeerPublicKey, req.EndpointHost, req.EndpointPort, req.DryRun)
	case "runtime":
		result, err = WGSetEndpoint(req.Interface, req.PeerPublicKey, endpoint, req.DryRun)
	default:
		err = fmt.Errorf("unsupported config_type: %s", req.ConfigType)
	}
	if err != nil {
		return ApplyResult{}, err
	}
	if !req.DryRun && req.ReloadMethod != "" && req.ReloadMethod != "none" {
		if err := ReloadInterface(req.Interface, req.ReloadMethod); err != nil {
			return ApplyResult{}, err
		}
	}
	return result, nil
}

func UpdateWGConf(path, peerPublicKey, endpoint string, dryRun bool) (ApplyResult, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ApplyResult{}, err
	}
	lines := splitKeepNewline(raw)
	inPeer := false
	matchingPeer := false
	endpointIndex := -1
	insertIndex := -1

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			if inPeer && matchingPeer {
				insertIndex = i
				break
			}
			inPeer = strings.EqualFold(trimmed, "[Peer]")
			matchingPeer = false
			endpointIndex = -1
			continue
		}
		if !inPeer || strings.HasPrefix(trimmed, "#") || !strings.Contains(trimmed, "=") {
			continue
		}
		parts := strings.SplitN(trimmed, "=", 2)
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		switch {
		case strings.EqualFold(key, "PublicKey") && value == peerPublicKey:
			matchingPeer = true
		case strings.EqualFold(key, "Endpoint") && matchingPeer:
			endpointIndex = i
		}
	}
	if inPeer && matchingPeer && insertIndex == -1 {
		insertIndex = len(lines)
	}
	if !matchingPeer {
		return ApplyResult{}, errors.New("peer public key not found in wg config")
	}

	newLine := "Endpoint = " + endpoint + "\n"
	if endpointIndex >= 0 {
		if strings.TrimSpace(lines[endpointIndex]) == strings.TrimSpace(newLine) {
			return ApplyResult{Changed: false, Message: "endpoint already up to date"}, nil
		}
		lines[endpointIndex] = newLine
	} else {
		lines = append(lines[:insertIndex], append([]string{newLine}, lines[insertIndex:]...)...)
	}
	if dryRun {
		return ApplyResult{Changed: true, Message: "dry-run: wg.conf would be updated"}, nil
	}
	backup := fmt.Sprintf("%s.bak.%d", path, time.Now().Unix())
	if err := os.WriteFile(backup, raw, 0o600); err != nil {
		return ApplyResult{}, err
	}
	return ApplyResult{Changed: true, Message: "updated " + path}, os.WriteFile(path, []byte(strings.Join(lines, "")), 0o600)
}

func UpdateOpenWrtUCI(interfaceName, peerPublicKey, endpointHost string, endpointPort int, dryRun bool) (ApplyResult, error) {
	if dryRun {
		return ApplyResult{Changed: true, Message: "dry-run: would update OpenWrt " + interfaceName + " peer endpoint"}, nil
	}
	section, err := findOpenWrtPeerSection(interfaceName, peerPublicKey)
	if err != nil {
		return ApplyResult{}, err
	}
	if err := run("uci", "set", fmt.Sprintf("network.%s.endpoint_host=%s", section, endpointHost)); err != nil {
		return ApplyResult{}, err
	}
	if err := run("uci", "set", fmt.Sprintf("network.%s.endpoint_port=%d", section, endpointPort)); err != nil {
		return ApplyResult{}, err
	}
	if err := run("uci", "commit", "network"); err != nil {
		return ApplyResult{}, err
	}
	return ApplyResult{Changed: true, Message: "updated OpenWrt section " + section}, nil
}

func WGSetEndpoint(interfaceName, peerPublicKey, endpoint string, dryRun bool) (ApplyResult, error) {
	if dryRun {
		return ApplyResult{Changed: true, Message: "dry-run: runtime endpoint would be updated"}, nil
	}
	if err := run("wg", "set", interfaceName, "peer", peerPublicKey, "endpoint", endpoint); err != nil {
		return ApplyResult{}, err
	}
	return ApplyResult{Changed: true, Message: "updated runtime endpoint"}, nil
}

func ReloadInterface(interfaceName, method string) error {
	switch method {
	case "ifup":
		_ = run("ifdown", interfaceName)
		return run("ifup", interfaceName)
	case "wg-quick-restart":
		return run("systemctl", "restart", "wg-quick@"+interfaceName)
	case "network-reload":
		return run("/etc/init.d/network", "reload")
	default:
		return fmt.Errorf("unsupported reload_method: %s", method)
	}
}

func StopInterface(interfaceName, method string) error {
	switch method {
	case "ifup", "openwrt":
		return run("ifdown", interfaceName)
	case "wg-quick":
		return run("wg-quick", "down", interfaceName)
	case "systemd":
		return run("systemctl", "stop", "wg-quick@"+interfaceName)
	default:
		return fmt.Errorf("unsupported wireguard_control_method: %s", method)
	}
}

func StartInterface(interfaceName, method string) error {
	switch method {
	case "ifup", "openwrt":
		return run("ifup", interfaceName)
	case "wg-quick":
		return run("wg-quick", "up", interfaceName)
	case "systemd":
		return run("systemctl", "start", "wg-quick@"+interfaceName)
	default:
		return fmt.Errorf("unsupported wireguard_control_method: %s", method)
	}
}

func findOpenWrtPeerSection(interfaceName, peerPublicKey string) (string, error) {
	cmd := exec.Command("uci", "show", "network")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	prefix := "network.@wireguard_" + interfaceName + "["
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, prefix) || !strings.Contains(line, ".public_key=") {
			continue
		}
		left, right, _ := strings.Cut(line, "=")
		value := strings.Trim(right, "'\"")
		if value == peerPublicKey {
			return strings.TrimPrefix(strings.TrimSuffix(left, ".public_key"), "network."), nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", errors.New("peer public key not found in OpenWrt UCI network config")
}

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s failed: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func splitKeepNewline(raw []byte) []string {
	if len(raw) == 0 {
		return nil
	}
	parts := strings.SplitAfter(string(raw), "\n")
	if parts[len(parts)-1] == "" {
		return parts[:len(parts)-1]
	}
	return parts
}
