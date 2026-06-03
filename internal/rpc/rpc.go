package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/yfy/wireguard-natter-helper/internal/protocol"
	"github.com/yfy/wireguard-natter-helper/internal/store"
)

type Request struct {
	Kind       string         `json:"kind"`
	NodeID     string         `json:"node_id,omitempty"`
	Token      string         `json:"token,omitempty"`
	JoinCode   string         `json:"join_code,omitempty"`
	Name       string         `json:"name,omitempty"`
	AdminToken string         `json:"admin_token,omitempty"`
	Meta       map[string]any `json:"meta,omitempty"`

	ReportType string         `json:"report_type,omitempty"`
	CommandID  string         `json:"command_id,omitempty"`
	Payload    map[string]any `json:"payload,omitempty"`

	ServerNodeID              string   `json:"server_node_id,omitempty"`
	ServerInterface           string   `json:"server_interface,omitempty"`
	DomainID                  string   `json:"domain_id,omitempty"`
	Description               string   `json:"description,omitempty"`
	Role                      string   `json:"role,omitempty"`
	NodeType                  string   `json:"node_type,omitempty"`
	Interface                 string   `json:"interface,omitempty"`
	ConfigType                string   `json:"config_type,omitempty"`
	ReloadMethod              string   `json:"reload_method,omitempty"`
	NatterCommand             []string `json:"natter_command,omitempty"`
	NatterTimeoutSeconds      int      `json:"natter_timeout_seconds,omitempty"`
	NatterStopWireGuard       bool     `json:"natter_stop_wireguard,omitempty"`
	NatterWireGuardControl    string   `json:"natter_wireguard_control,omitempty"`
	NatterRestartDelaySeconds int      `json:"natter_restart_delay_seconds,omitempty"`
	Limit                     int      `json:"limit,omitempty"`
}

type Response struct {
	OK           bool                `json:"ok"`
	Error        string              `json:"error,omitempty"`
	Command      *protocol.Command   `json:"command,omitempty"`
	MonitorPeers []MonitorPeer       `json:"monitor_peers,omitempty"`
	Queued       int                 `json:"queued,omitempty"`
	Approved     bool                `json:"approved,omitempty"`
	Domain       *store.Domain       `json:"domain,omitempty"`
	Domains      []store.Domain      `json:"domains,omitempty"`
	Nodes        []store.Node        `json:"nodes,omitempty"`
	Bindings     []store.Binding     `json:"bindings,omitempty"`
	WGInterfaces []store.WGInterface `json:"wireguard_interfaces,omitempty"`
	Events       []store.Event       `json:"events,omitempty"`
}

type MonitorPeer struct {
	BindingID       string `json:"binding_id"`
	Interface       string `json:"interface"`
	PeerPublicKey   string `json:"peer_public_key"`
	ServerNodeID    string `json:"server_node_id"`
	ServerInterface string `json:"server_interface"`
}

func NormalizeAddr(addr string) (string, error) {
	addr = strings.TrimSpace(addr)
	addr = strings.TrimPrefix(addr, "tcp://")
	if strings.HasPrefix(addr, "http://") || strings.HasPrefix(addr, "https://") {
		return "", fmt.Errorf("HTTP URLs are not supported; use host:port or tcp://host:port")
	}
	if addr == "" {
		return "", fmt.Errorf("empty daemon address")
	}
	return addr, nil
}

func Call(ctx context.Context, addr string, req Request, timeout time.Duration) (Response, error) {
	addr, err := NormalizeAddr(addr)
	if err != nil {
		return Response{}, err
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	dialer := net.Dialer{}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return Response{}, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return Response{}, err
	}
	var resp Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return Response{}, err
	}
	if !resp.OK {
		if resp.Error == "" {
			resp.Error = "daemon returned error"
		}
		return resp, errors.New(resp.Error)
	}
	return resp, nil
}
