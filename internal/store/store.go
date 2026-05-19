package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/yfy/wireguard-natter-helper/internal/auth"
	"github.com/yfy/wireguard-natter-helper/internal/protocol"
)

type Store struct {
	path string
	mu   sync.Mutex
	data Data
}

type Data struct {
	Nodes          map[string]Node            `json:"nodes"`
	Bindings       map[string]Binding         `json:"bindings"`
	EndpointLeases []EndpointLease            `json:"endpoint_leases"`
	Commands       map[string][]QueuedCommand `json:"commands"`
	Events         []Event                    `json:"events"`
}

type Node struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Role         string `json:"role"`
	TokenHash    string `json:"token_hash"`
	Status       string `json:"status"`
	Platform     string `json:"platform"`
	AgentVersion string `json:"agent_version"`
	LastSeenAt   string `json:"last_seen_at"`
}

type Binding struct {
	ID              string `json:"id"`
	ServerNodeID    string `json:"server_node_id"`
	ServerInterface string `json:"server_interface"`
	ClientNodeID    string `json:"client_node_id"`
	ClientInterface string `json:"client_interface"`
	PeerPublicKey   string `json:"peer_public_key"`
	ConfigType      string `json:"config_type"`
	ConfigPath      string `json:"config_path"`
	ReloadMethod    string `json:"reload_method"`
	EndpointHost    string `json:"endpoint_host"`
	EndpointPort    int    `json:"endpoint_port"`
	Enabled         bool   `json:"enabled"`
}

type EndpointLease struct {
	ServerNodeID    string `json:"server_node_id"`
	ServerInterface string `json:"server_interface"`
	Protocol        string `json:"protocol"`
	LocalIP         string `json:"local_ip"`
	LocalPort       int    `json:"local_port"`
	PublicIP        string `json:"public_ip"`
	PublicPort      int    `json:"public_port"`
	CreatedAt       string `json:"created_at"`
}

type QueuedCommand struct {
	Command   protocol.Command `json:"command"`
	Status    string           `json:"status"`
	Result    map[string]any   `json:"result,omitempty"`
	CreatedAt string           `json:"created_at"`
}

type Event struct {
	Type      string         `json:"type"`
	Severity  string         `json:"severity"`
	NodeID    string         `json:"node_id,omitempty"`
	BindingID string         `json:"binding_id,omitempty"`
	Message   string         `json:"message"`
	Payload   map[string]any `json:"payload,omitempty"`
	CreatedAt string         `json:"created_at"`
}

func Open(path string) (*Store, error) {
	s := &Store{path: path, data: newData()}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil && filepath.Dir(path) != "." {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return s, s.Save()
		}
		return nil, err
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &s.data); err != nil {
			return nil, err
		}
	}
	s.ensureMaps()
	return s, nil
}

func newData() Data {
	return Data{
		Nodes:    map[string]Node{},
		Bindings: map[string]Binding{},
		Commands: map[string][]QueuedCommand{},
		Events:   []Event{},
	}
}

func (s *Store) ensureMaps() {
	if s.data.Nodes == nil {
		s.data.Nodes = map[string]Node{}
	}
	if s.data.Bindings == nil {
		s.data.Bindings = map[string]Binding{}
	}
	if s.data.Commands == nil {
		s.data.Commands = map[string][]QueuedCommand{}
	}
}

func (s *Store) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked()
}

func (s *Store) saveLocked() error {
	raw, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *Store) CreateNode(id, name, role, token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Nodes[id] = Node{ID: id, Name: name, Role: role, TokenHash: auth.HashToken(token), Status: "offline"}
	s.addEventLocked("node.upserted", "info", id, "", "Node saved", nil)
	return s.saveLocked()
}

func (s *Store) AuthenticateNode(id, token string) (Node, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	node, ok := s.data.Nodes[id]
	if !ok || !auth.VerifyToken(token, node.TokenHash) {
		return Node{}, false
	}
	return node, true
}

func (s *Store) MarkSeen(id string, meta map[string]any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	node := s.data.Nodes[id]
	node.Status = "online"
	node.LastSeenAt = protocol.NowISO()
	node.Platform, _ = meta["platform"].(string)
	node.AgentVersion, _ = meta["agent_version"].(string)
	s.data.Nodes[id] = node
	return s.saveLocked()
}

func (s *Store) AddBinding(b Binding) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if b.ConfigType == "" {
		b.ConfigType = "openwrt_uci"
	}
	if b.ReloadMethod == "" {
		b.ReloadMethod = "none"
	}
	b.Enabled = true
	s.data.Bindings[b.ID] = b
	s.addEventLocked("binding.upserted", "info", "", b.ID, "Binding saved", nil)
	return s.saveLocked()
}

func (s *Store) QueueCommand(nodeID string, cmd protocol.Command) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Commands[nodeID] = append(s.data.Commands[nodeID], QueuedCommand{Command: cmd, Status: "pending", CreatedAt: protocol.NowISO()})
	s.addEventLocked("command.queued", "info", nodeID, "", "Command queued", map[string]any{"action": cmd.Action})
	return s.saveLocked()
}

func (s *Store) NextCommand(nodeID string) (*protocol.Command, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	queue := s.data.Commands[nodeID]
	for i := range queue {
		if queue[i].Status == "pending" {
			queue[i].Status = "delivered"
			s.data.Commands[nodeID] = queue
			cmd := queue[i].Command
			return &cmd, s.saveLocked()
		}
	}
	return nil, nil
}

func (s *Store) CompleteCommand(nodeID, commandID string, result map[string]any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	queue := s.data.Commands[nodeID]
	for i := range queue {
		if queue[i].Command.CommandID == commandID {
			queue[i].Status = "completed"
			queue[i].Result = result
			break
		}
	}
	s.data.Commands[nodeID] = queue
	s.addEventLocked("command.completed", "info", nodeID, "", "Command completed", result)
	return s.saveLocked()
}

func (s *Store) SaveEndpointLease(nodeID string, lease EndpointLease) ([]Binding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	lease.ServerNodeID = nodeID
	lease.CreatedAt = protocol.NowISO()
	s.data.EndpointLeases = append(s.data.EndpointLeases, lease)
	var bindings []Binding
	for id, b := range s.data.Bindings {
		if b.Enabled && b.ServerNodeID == nodeID && b.ServerInterface == lease.ServerInterface {
			b.EndpointHost = lease.PublicIP
			b.EndpointPort = lease.PublicPort
			s.data.Bindings[id] = b
			bindings = append(bindings, b)
		}
	}
	s.addEventLocked("endpoint.updated", "info", nodeID, "", "Endpoint lease saved", nil)
	return bindings, s.saveLocked()
}

func (s *Store) Nodes() []Node {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Node, 0, len(s.data.Nodes))
	for _, n := range s.data.Nodes {
		n.TokenHash = ""
		out = append(out, n)
	}
	return out
}

func (s *Store) Bindings() []Binding {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Binding, 0, len(s.data.Bindings))
	for _, b := range s.data.Bindings {
		out = append(out, b)
	}
	return out
}

func (s *Store) Events(limit int) []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit <= 0 || limit > len(s.data.Events) {
		limit = len(s.data.Events)
	}
	out := make([]Event, 0, limit)
	for i := len(s.data.Events) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, s.data.Events[i])
	}
	return out
}

func (s *Store) AddEvent(eventType, severity, nodeID, bindingID, message string, payload map[string]any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.addEventLocked(eventType, severity, nodeID, bindingID, message, payload)
	return s.saveLocked()
}

func (s *Store) addEventLocked(eventType, severity, nodeID, bindingID, message string, payload map[string]any) {
	s.data.Events = append(s.data.Events, Event{
		Type: eventType, Severity: severity, NodeID: nodeID, BindingID: bindingID,
		Message: message, Payload: payload, CreatedAt: protocol.NowISO(),
	})
}
