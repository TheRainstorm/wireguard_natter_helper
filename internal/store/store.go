package store

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"github.com/yfy/wireguard-natter-helper/internal/auth"
	"github.com/yfy/wireguard-natter-helper/internal/protocol"
)

type Store struct {
	path        string
	runtimePath string
	eventPath   string
	mu          sync.Mutex
	data        Data
	runtime     RuntimeData
}

const maxEventLogBytes = 1 << 20

type Data struct {
	Nodes         map[string]Node         `json:"nodes"`
	Domains       map[string]Domain       `json:"domains"`
	DomainMembers map[string]DomainMember `json:"domain_members"`
	Bindings      map[string]Binding      `json:"bindings"`

	// Deprecated state fields are read for migration, then omitted from future state saves.
	WGInterfaces   map[string]WGInterface     `json:"wireguard_interfaces,omitempty"`
	EndpointLeases []EndpointLease            `json:"endpoint_leases,omitempty"`
	Commands       map[string][]QueuedCommand `json:"commands,omitempty"`
	Events         []Event                    `json:"events,omitempty"`
}

type Node struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	TokenHash string `json:"token_hash,omitempty"`
	Approved  bool   `json:"approved"`

	// Deprecated node-level config fields are kept only to migrate older state files.
	Role                      string   `json:"role,omitempty"`
	DomainID                  string   `json:"domain_id,omitempty"`
	NodeType                  string   `json:"node_type,omitempty"`
	Interface                 string   `json:"interface,omitempty"`
	ConfigType                string   `json:"config_type,omitempty"`
	ReloadMethod              string   `json:"reload_method,omitempty"`
	NatterManaged             bool     `json:"natter_managed,omitempty"`
	NatterConfigured          bool     `json:"natter_configured,omitempty"`
	NatterCommand             []string `json:"natter_command,omitempty"`
	NatterTimeoutSeconds      int      `json:"natter_timeout_seconds,omitempty"`
	NatterStopWireGuard       bool     `json:"natter_stop_wireguard,omitempty"`
	NatterWireGuardControl    string   `json:"natter_wireguard_control,omitempty"`
	NatterRestartDelaySeconds int      `json:"natter_restart_delay_seconds,omitempty"`

	Status       string `json:"status,omitempty"`
	Platform     string `json:"platform,omitempty"`
	AgentVersion string `json:"agent_version,omitempty"`
	LastSeenAt   string `json:"last_seen_at,omitempty"`
}

type Domain struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	JoinCode    string `json:"join_code,omitempty"`
	CreatedAt   string `json:"created_at"`
	Description string `json:"description,omitempty"`
}

type DomainMember struct {
	DomainID                  string   `json:"domain_id"`
	NodeID                    string   `json:"node_id"`
	Role                      string   `json:"role"`
	NodeType                  string   `json:"node_type"`
	Interface                 string   `json:"interface"`
	ConfigType                string   `json:"config_type"`
	ReloadMethod              string   `json:"reload_method"`
	NatterManaged             bool     `json:"natter_managed,omitempty"`
	NatterConfigured          bool     `json:"natter_configured,omitempty"`
	NatterCommand             []string `json:"natter_command,omitempty"`
	NatterTimeoutSeconds      int      `json:"natter_timeout_seconds,omitempty"`
	NatterStopWireGuard       bool     `json:"natter_stop_wireguard,omitempty"`
	NatterWireGuardControl    string   `json:"natter_wireguard_control,omitempty"`
	NatterRestartDelaySeconds int      `json:"natter_restart_delay_seconds,omitempty"`
	UpdatedAt                 string   `json:"updated_at"`
}

type Binding struct {
	ID              string `json:"id"`
	DomainID        string `json:"domain_id,omitempty"`
	Auto            bool   `json:"auto,omitempty"`
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

type WGInterface struct {
	NodeID     string   `json:"node_id"`
	Name       string   `json:"name"`
	PublicKey  string   `json:"public_key,omitempty"`
	ListenPort int      `json:"listen_port,omitempty"`
	Peers      []string `json:"peers,omitempty"`
	ConfigType string   `json:"config_type,omitempty"`
	ConfigPath string   `json:"config_path,omitempty"`
	UpdatedAt  string   `json:"updated_at"`
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

type RuntimeData struct {
	NodeRuntime     map[string]NodeRuntime     `json:"node_runtime"`
	PendingDomain   map[string]string          `json:"pending_domain,omitempty"`
	WGInterfaces    map[string]WGInterface     `json:"wireguard_interfaces"`
	EndpointLeases  []EndpointLease            `json:"endpoint_leases,omitempty"`
	BindingEndpoint map[string]BindingEndpoint `json:"binding_endpoint,omitempty"`
	Commands        map[string][]QueuedCommand `json:"commands"`
}

type NodeRuntime struct {
	Status       string `json:"status"`
	Platform     string `json:"platform,omitempty"`
	AgentVersion string `json:"agent_version,omitempty"`
	LastSeenAt   string `json:"last_seen_at,omitempty"`
}

type BindingEndpoint struct {
	Host      string `json:"host"`
	Port      int    `json:"port"`
	UpdatedAt string `json:"updated_at"`
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
	Actor     string         `json:"actor,omitempty"`
	Target    string         `json:"target,omitempty"`
	Action    string         `json:"action,omitempty"`
	Before    map[string]any `json:"before,omitempty"`
	After     map[string]any `json:"after,omitempty"`
	Result    string         `json:"result,omitempty"`
	Error     string         `json:"error,omitempty"`
	NodeID    string         `json:"node_id,omitempty"`
	BindingID string         `json:"binding_id,omitempty"`
	Message   string         `json:"message"`
	Payload   map[string]any `json:"payload,omitempty"`
	CreatedAt string         `json:"created_at"`
}

func Open(path string) (*Store, error) {
	s := &Store{
		path:        path,
		runtimePath: path + ".runtime.json",
		eventPath:   path + ".events.jsonl",
		data:        newData(),
		runtime:     newRuntimeData(),
	}
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
	if err := s.loadRuntime(); err != nil {
		return nil, err
	}
	if err := s.ensureMaps(); err != nil {
		return nil, err
	}
	if err := s.Save(); err != nil {
		return nil, err
	}
	return s, nil
}

func newData() Data {
	return Data{
		Nodes:         map[string]Node{},
		Domains:       map[string]Domain{},
		DomainMembers: map[string]DomainMember{},
		Bindings:      map[string]Binding{},
	}
}

func newRuntimeData() RuntimeData {
	return RuntimeData{
		NodeRuntime:     map[string]NodeRuntime{},
		PendingDomain:   map[string]string{},
		WGInterfaces:    map[string]WGInterface{},
		BindingEndpoint: map[string]BindingEndpoint{},
		Commands:        map[string][]QueuedCommand{},
	}
}

func (s *Store) ensureMaps() error {
	if s.data.Nodes == nil {
		s.data.Nodes = map[string]Node{}
	}
	if s.data.Domains == nil {
		s.data.Domains = map[string]Domain{}
	}
	if s.data.DomainMembers == nil {
		s.data.DomainMembers = map[string]DomainMember{}
	}
	if s.data.Bindings == nil {
		s.data.Bindings = map[string]Binding{}
	}
	s.ensureRuntimeMaps()
	if s.data.WGInterfaces == nil {
		s.data.WGInterfaces = map[string]WGInterface{}
	}
	if s.data.Commands == nil {
		s.data.Commands = map[string][]QueuedCommand{}
	}
	for _, node := range s.data.Nodes {
		if node.DomainID == "" || node.Interface == "" || node.Role == "" {
			continue
		}
		key := domainMemberKey(node.DomainID, node.ID)
		if _, exists := s.data.DomainMembers[key]; exists {
			continue
		}
		member := DomainMember{
			DomainID:                  node.DomainID,
			NodeID:                    node.ID,
			Role:                      node.Role,
			NodeType:                  node.NodeType,
			Interface:                 node.Interface,
			ConfigType:                node.ConfigType,
			ReloadMethod:              node.ReloadMethod,
			NatterManaged:             node.NatterManaged,
			NatterConfigured:          node.NatterConfigured,
			NatterCommand:             append([]string(nil), node.NatterCommand...),
			NatterTimeoutSeconds:      node.NatterTimeoutSeconds,
			NatterStopWireGuard:       node.NatterStopWireGuard,
			NatterWireGuardControl:    node.NatterWireGuardControl,
			NatterRestartDelaySeconds: node.NatterRestartDelaySeconds,
			UpdatedAt:                 node.LastSeenAt,
		}
		defaultMemberRuntimeFields(&member)
		s.data.DomainMembers[key] = member
	}
	for id, node := range s.data.Nodes {
		if node.Status != "" || node.LastSeenAt != "" || node.Platform != "" || node.AgentVersion != "" {
			s.runtime.NodeRuntime[id] = NodeRuntime{
				Status:       firstNonEmpty(node.Status, "offline"),
				Platform:     node.Platform,
				AgentVersion: node.AgentVersion,
				LastSeenAt:   node.LastSeenAt,
			}
		}
		clearNodeTransientFields(&node)
		s.data.Nodes[id] = node
	}
	for key, item := range s.data.WGInterfaces {
		s.runtime.WGInterfaces[key] = item
	}
	if len(s.data.Commands) > 0 {
		for nodeID, queue := range s.data.Commands {
			s.runtime.Commands[nodeID] = append([]QueuedCommand(nil), queue...)
		}
	}
	if len(s.data.EndpointLeases) > 0 {
		s.runtime.EndpointLeases = append([]EndpointLease(nil), s.data.EndpointLeases...)
	}
	now := protocol.NowISO()
	for id, binding := range s.data.Bindings {
		if binding.EndpointHost != "" && binding.EndpointPort > 0 {
			s.runtime.BindingEndpoint[id] = BindingEndpoint{Host: binding.EndpointHost, Port: binding.EndpointPort, UpdatedAt: now}
			binding.EndpointHost = ""
			binding.EndpointPort = 0
			s.data.Bindings[id] = binding
		}
	}
	if len(s.data.Events) > 0 {
		for _, event := range s.data.Events {
			if err := s.appendEventFileLocked(event); err != nil {
				return err
			}
		}
	}
	s.clearDeprecatedStateFields()
	return nil
}

func (s *Store) ensureRuntimeMaps() {
	if s.runtime.NodeRuntime == nil {
		s.runtime.NodeRuntime = map[string]NodeRuntime{}
	}
	if s.runtime.PendingDomain == nil {
		s.runtime.PendingDomain = map[string]string{}
	}
	if s.runtime.WGInterfaces == nil {
		s.runtime.WGInterfaces = map[string]WGInterface{}
	}
	if s.runtime.BindingEndpoint == nil {
		s.runtime.BindingEndpoint = map[string]BindingEndpoint{}
	}
	if s.runtime.Commands == nil {
		s.runtime.Commands = map[string][]QueuedCommand{}
	}
}

func (s *Store) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.saveLocked(); err != nil {
		return err
	}
	return s.saveRuntimeLocked()
}

func (s *Store) saveLocked() error {
	data := s.data
	data.WGInterfaces = nil
	data.EndpointLeases = nil
	data.Commands = nil
	data.Events = nil
	data.Nodes = make(map[string]Node, len(s.data.Nodes))
	for id, node := range s.data.Nodes {
		clearNodeTransientFields(&node)
		data.Nodes[id] = node
	}
	data.Bindings = make(map[string]Binding, len(s.data.Bindings))
	for id, binding := range s.data.Bindings {
		binding.EndpointHost = ""
		binding.EndpointPort = 0
		data.Bindings[id] = binding
	}
	raw, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *Store) loadRuntime() error {
	raw, err := os.ReadFile(s.runtimePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, &s.runtime); err != nil {
		return err
	}
	return nil
}

func (s *Store) saveRuntimeLocked() error {
	s.ensureRuntimeMaps()
	raw, err := json.MarshalIndent(s.runtime, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.runtimePath + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.runtimePath)
}

func (s *Store) clearDeprecatedStateFields() {
	s.data.WGInterfaces = nil
	s.data.EndpointLeases = nil
	s.data.Commands = nil
	s.data.Events = nil
}

func (s *Store) CreateNode(id, name, token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Nodes[id] = Node{ID: id, Name: name, TokenHash: auth.HashToken(token), Approved: true}
	s.runtime.NodeRuntime[id] = NodeRuntime{Status: "offline"}
	s.addAuditEventLocked(Event{
		Type: "node.upserted", Severity: "info", Actor: "admin", Target: "node:" + id, Action: "node.create",
		After:  map[string]any{"node_id": id, "name": name, "approved": true},
		Result: "success", NodeID: id, Message: fmt.Sprintf("Node %s saved", label(name, id)),
	})
	if err := s.saveLocked(); err != nil {
		return err
	}
	return s.saveRuntimeLocked()
}

func (s *Store) AuthenticateNode(id, token string) (Node, bool) {
	node, ok := s.AuthenticateNodeAnyStatus(id, token)
	if !ok || !node.Approved {
		return Node{}, false
	}
	return node, true
}

func (s *Store) AuthenticateNodeAnyStatus(id, token string) (Node, bool) {
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
	runtime := s.runtime.NodeRuntime[id]
	if node.Approved {
		runtime.Status = "online"
	} else {
		runtime.Status = "pending"
	}
	runtime.LastSeenAt = protocol.NowISO()
	if meta != nil {
		runtime.Platform, _ = meta["platform"].(string)
		runtime.AgentVersion, _ = meta["agent_version"].(string)
	}
	s.runtime.NodeRuntime[id] = runtime
	return s.saveRuntimeLocked()
}

func (s *Store) CreateDomain(id, name, joinCode, description string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == "" {
		return errors.New("domain id is required")
	}
	if name == "" {
		name = id
	}
	if _, exists := s.data.Domains[id]; exists {
		return errors.New("domain already exists")
	}
	s.data.Domains[id] = Domain{ID: id, Name: name, JoinCode: joinCode, Description: description, CreatedAt: protocol.NowISO()}
	s.addAuditEventLocked(Event{
		Type: "domain.created", Severity: "info", Actor: "admin", Target: "domain:" + id, Action: "domain.create",
		After:  map[string]any{"domain_id": id, "name": name, "description": description},
		Result: "success", Message: fmt.Sprintf("Domain %s created", id),
		Payload: map[string]any{"domain_id": id, "name": name},
	})
	return s.saveLocked()
}

func (s *Store) Domains(includeSecrets bool) []Domain {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Domain, 0, len(s.data.Domains))
	for _, domain := range s.data.Domains {
		if !includeSecrets {
			domain.JoinCode = ""
		}
		out = append(out, domain)
	}
	return out
}

func (s *Store) DomainByJoinCode(joinCode string) (Domain, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, domain := range s.data.Domains {
		if domain.JoinCode != "" && domain.JoinCode == joinCode {
			return domain, true
		}
	}
	return Domain{}, false
}

func (s *Store) UpsertJoinedNode(domainID, nodeID, name, token string, meta map[string]any) (Node, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if nodeID == "" || token == "" {
		return Node{}, false, errors.New("node id and token are required")
	}
	if _, ok := s.data.Domains[domainID]; !ok {
		return Node{}, false, errors.New("domain not found")
	}
	if existing, ok := s.data.Nodes[nodeID]; ok {
		if !auth.VerifyToken(token, existing.TokenHash) {
			return Node{}, false, errors.New("node id already exists with a different token")
		}
		runtime := s.runtime.NodeRuntime[nodeID]
		runtime.LastSeenAt = protocol.NowISO()
		runtime.Status = "pending"
		s.runtime.PendingDomain[nodeID] = domainID
		if meta != nil {
			runtime.Platform, _ = meta["platform"].(string)
			runtime.AgentVersion, _ = meta["agent_version"].(string)
		}
		s.runtime.NodeRuntime[nodeID] = runtime
		return s.nodeWithRuntime(existing), false, s.saveRuntimeLocked()
	}
	node := Node{
		ID:        nodeID,
		Name:      name,
		TokenHash: auth.HashToken(token),
		Approved:  false,
	}
	if node.Name == "" {
		node.Name = node.ID
	}
	runtime := NodeRuntime{Status: "pending", LastSeenAt: protocol.NowISO()}
	if meta != nil {
		runtime.Platform, _ = meta["platform"].(string)
		runtime.AgentVersion, _ = meta["agent_version"].(string)
	}
	s.data.Nodes[node.ID] = node
	s.runtime.NodeRuntime[node.ID] = runtime
	s.runtime.PendingDomain[node.ID] = domainID
	s.addAuditEventLocked(Event{
		Type: "node.joined", Severity: "info", Actor: "agent:" + node.ID, Target: "domain:" + domainID, Action: "node.join",
		After:  map[string]any{"node_id": node.ID, "node_name": node.Name, "domain_id": domainID, "status": "pending"},
		Result: "success", NodeID: node.ID, Message: fmt.Sprintf("Node %s joined domain %s and is pending approval", label(node.Name, node.ID), domainID),
		Payload: map[string]any{"domain_id": domainID, "node_name": node.Name},
	})
	if err := s.saveLocked(); err != nil {
		return Node{}, false, err
	}
	return s.nodeWithRuntime(node), true, s.saveRuntimeLocked()
}

func (s *Store) UpsertPendingNode(nodeID, name, token string, meta map[string]any) (Node, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if nodeID == "" || token == "" {
		return Node{}, false, errors.New("node id and token are required")
	}
	if existing, ok := s.data.Nodes[nodeID]; ok {
		if !auth.VerifyToken(token, existing.TokenHash) {
			return Node{}, false, errors.New("node id already exists with a different token")
		}
		if existing.Name == "" && name != "" {
			existing.Name = name
		}
		runtime := s.runtime.NodeRuntime[nodeID]
		runtime.LastSeenAt = protocol.NowISO()
		runtime.Status = "pending"
		if meta != nil {
			runtime.Platform, _ = meta["platform"].(string)
			runtime.AgentVersion, _ = meta["agent_version"].(string)
		}
		s.data.Nodes[nodeID] = existing
		s.runtime.NodeRuntime[nodeID] = runtime
		if err := s.saveLocked(); err != nil {
			return Node{}, false, err
		}
		return s.nodeWithRuntime(existing), false, s.saveRuntimeLocked()
	}
	node := Node{
		ID:        nodeID,
		Name:      name,
		TokenHash: auth.HashToken(token),
		Approved:  false,
	}
	if node.Name == "" {
		node.Name = node.ID
	}
	runtime := NodeRuntime{Status: "pending", LastSeenAt: protocol.NowISO()}
	if meta != nil {
		runtime.Platform, _ = meta["platform"].(string)
		runtime.AgentVersion, _ = meta["agent_version"].(string)
	}
	s.data.Nodes[node.ID] = node
	s.runtime.NodeRuntime[node.ID] = runtime
	s.addAuditEventLocked(Event{
		Type: "node.registered", Severity: "info", Actor: "agent:" + node.ID, Target: "node:" + node.ID, Action: "node.register",
		After:  map[string]any{"node_id": node.ID, "node_name": node.Name, "status": "pending"},
		Result: "success", NodeID: node.ID, Message: fmt.Sprintf("Node %s registered and is pending approval", label(node.Name, node.ID)),
		Payload: map[string]any{"node_name": node.Name},
	})
	if err := s.saveLocked(); err != nil {
		return Node{}, false, err
	}
	return s.nodeWithRuntime(node), true, s.saveRuntimeLocked()
}

type NodeApproval struct {
	DomainID                  string
	Role                      string
	NodeType                  string
	Interface                 string
	ConfigType                string
	ReloadMethod              string
	NatterManaged             bool
	NatterConfigured          bool
	NatterCommand             []string
	NatterTimeoutSeconds      int
	NatterStopWireGuard       bool
	NatterWireGuardControl    string
	NatterRestartDelaySeconds int
	Name                      string
}

func (s *Store) ApproveNode(nodeID string, approval NodeApproval) (Node, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	node, ok := s.data.Nodes[nodeID]
	if !ok {
		return Node{}, errors.New("node not found")
	}
	domainID := firstNonEmpty(approval.DomainID, s.runtime.PendingDomain[nodeID], singleMemberDomainID(s.data.DomainMembers, nodeID))
	if domainID == "" {
		return Node{}, errors.New("domain is required")
	}
	if _, ok := s.data.Domains[domainID]; !ok {
		return Node{}, errors.New("domain not found")
	}
	if approval.Name != "" {
		node.Name = approval.Name
	}
	member := s.data.DomainMembers[domainMemberKey(domainID, nodeID)]
	before := domainMemberAudit(member)
	member.DomainID = domainID
	member.NodeID = nodeID
	member.Role = firstNonEmpty(approval.Role, member.Role)
	member.NodeType = firstNonEmpty(approval.NodeType, member.NodeType)
	member.Interface = firstNonEmpty(approval.Interface, member.Interface)
	member.ConfigType = firstNonEmpty(approval.ConfigType, member.ConfigType)
	member.ReloadMethod = firstNonEmpty(approval.ReloadMethod, member.ReloadMethod)
	if approval.NatterManaged {
		member.NatterManaged = true
		if approval.NatterConfigured {
			member.NatterCommand = append([]string(nil), approval.NatterCommand...)
			member.NatterConfigured = true
			member.NatterStopWireGuard = approval.NatterStopWireGuard
			member.NatterTimeoutSeconds = approval.NatterTimeoutSeconds
			member.NatterWireGuardControl = approval.NatterWireGuardControl
			member.NatterRestartDelaySeconds = approval.NatterRestartDelaySeconds
		} else {
			member.NatterConfigured = false
			member.NatterCommand = nil
			member.NatterTimeoutSeconds = 0
			member.NatterStopWireGuard = false
			member.NatterWireGuardControl = ""
			member.NatterRestartDelaySeconds = 0
		}
	}
	if member.Role == "" {
		return Node{}, errors.New("role is required")
	}
	if member.Interface == "" {
		return Node{}, errors.New("interface is required")
	}
	defaultMemberRuntimeFields(&member)
	member.UpdatedAt = protocol.NowISO()
	s.data.DomainMembers[domainMemberKey(domainID, nodeID)] = member

	wasApproved := node.Approved
	node.Approved = true
	delete(s.runtime.PendingDomain, nodeID)
	runtime := s.runtime.NodeRuntime[nodeID]
	if runtime.Status == "pending" || runtime.Status == "" {
		runtime.Status = "offline"
	}
	s.runtime.NodeRuntime[nodeID] = runtime
	clearNodeTransientFields(&node)
	s.data.Nodes[nodeID] = node
	eventType := "node.approved"
	message := fmt.Sprintf("Node %s approved in domain %s as %s on %s", label(node.Name, node.ID), member.DomainID, member.Role, member.Interface)
	if wasApproved {
		eventType = "node.updated"
		message = fmt.Sprintf("Node %s updated in domain %s: role=%s interface=%s node_type=%s", label(node.Name, node.ID), member.DomainID, member.Role, member.Interface, member.NodeType)
	}
	s.addAuditEventLocked(Event{
		Type: eventType, Severity: "info", Actor: "admin", Target: "membership:" + domainMemberKey(member.DomainID, member.NodeID), Action: "membership.save",
		Before: before, After: domainMemberAudit(member), Result: "success", NodeID: node.ID, Message: message,
		Payload: map[string]any{
			"domain_id":     member.DomainID,
			"role":          member.Role,
			"interface":     member.Interface,
			"node_type":     member.NodeType,
			"config_type":   member.ConfigType,
			"reload_method": member.ReloadMethod,
		},
	})
	if err := s.saveLocked(); err != nil {
		return Node{}, err
	}
	return s.nodeWithRuntime(node), s.saveRuntimeLocked()
}

func (s *Store) AddBinding(b Binding) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	defaultBindingFields(&b)
	b.Enabled = true
	endpointHost := b.EndpointHost
	endpointPort := b.EndpointPort
	b.EndpointHost = ""
	b.EndpointPort = 0
	s.data.Bindings[b.ID] = b
	if endpointHost != "" && endpointPort > 0 {
		s.runtime.BindingEndpoint[b.ID] = BindingEndpoint{Host: endpointHost, Port: endpointPort, UpdatedAt: protocol.NowISO()}
	}
	s.addAuditEventLocked(Event{
		Type: "binding.upserted", Severity: "info", Actor: "admin", Target: "binding:" + b.ID, Action: "binding.save",
		After: bindingAudit(b), Result: "success", BindingID: b.ID,
		Message: fmt.Sprintf("Binding %s saved: %s/%s -> %s/%s", b.ID, b.ServerNodeID, b.ServerInterface, b.ClientNodeID, b.ClientInterface),
		Payload: map[string]any{"domain_id": b.DomainID},
	})
	if err := s.saveLocked(); err != nil {
		return err
	}
	return s.saveRuntimeLocked()
}

func (s *Store) DeleteNode(nodeID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if nodeID == "" {
		return errors.New("node id is required")
	}
	node, ok := s.data.Nodes[nodeID]
	if !ok {
		return errors.New("node not found")
	}
	delete(s.data.Nodes, nodeID)
	delete(s.runtime.NodeRuntime, nodeID)
	delete(s.runtime.Commands, nodeID)
	for key, member := range s.data.DomainMembers {
		if member.NodeID == nodeID {
			delete(s.data.DomainMembers, key)
		}
	}
	for key, item := range s.runtime.WGInterfaces {
		if item.NodeID == nodeID {
			delete(s.runtime.WGInterfaces, key)
		}
	}
	for id, binding := range s.data.Bindings {
		if binding.ServerNodeID == nodeID || binding.ClientNodeID == nodeID {
			delete(s.data.Bindings, id)
			delete(s.runtime.BindingEndpoint, id)
		}
	}
	leases := s.runtime.EndpointLeases[:0]
	for _, lease := range s.runtime.EndpointLeases {
		if lease.ServerNodeID != nodeID {
			leases = append(leases, lease)
		}
	}
	s.runtime.EndpointLeases = leases
	s.addAuditEventLocked(Event{
		Type: "node.deleted", Severity: "info", Actor: "admin", Target: "node:" + nodeID, Action: "node.delete",
		Before: map[string]any{"node_id": node.ID, "name": node.Name}, Result: "success", NodeID: nodeID,
		Message: fmt.Sprintf("Node %s deleted with related memberships, commands, inventory, and bindings", label(node.Name, node.ID)),
		Payload: map[string]any{"name": node.Name},
	})
	if err := s.saveLocked(); err != nil {
		return err
	}
	return s.saveRuntimeLocked()
}

func (s *Store) UpdateWGInterfaces(nodeID string, interfaces []WGInterface) error {
	if nodeID == "" || len(interfaces) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := protocol.NowISO()
	for key, item := range s.runtime.WGInterfaces {
		if item.NodeID == nodeID {
			delete(s.runtime.WGInterfaces, key)
		}
	}
	for _, item := range interfaces {
		if item.Name == "" {
			continue
		}
		item.NodeID = nodeID
		item.UpdatedAt = now
		s.runtime.WGInterfaces[wgInterfaceKey(nodeID, item.Name)] = item
	}
	return s.saveRuntimeLocked()
}

func (s *Store) WGInterfaces() []WGInterface {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]WGInterface, 0, len(s.runtime.WGInterfaces))
	for _, item := range s.runtime.WGInterfaces {
		out = append(out, item)
	}
	return out
}

func (s *Store) DomainMembers() []DomainMember {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]DomainMember, 0, len(s.data.DomainMembers))
	for _, member := range s.data.DomainMembers {
		out = append(out, member)
	}
	return out
}

func (s *Store) DomainMembersForNode(nodeID string) []DomainMember {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []DomainMember
	for _, member := range s.data.DomainMembers {
		if member.NodeID == nodeID {
			out = append(out, member)
		}
	}
	return out
}

func (s *Store) ReconcileAutoBindings() ([]Binding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var created []Binding
	updated := 0
	validAuto := map[string]bool{}
	membersByDomain := map[string][]DomainMember{}
	for _, member := range s.data.DomainMembers {
		node, ok := s.data.Nodes[member.NodeID]
		if !ok || !node.Approved || member.DomainID == "" {
			continue
		}
		membersByDomain[member.DomainID] = append(membersByDomain[member.DomainID], member)
	}
	for domainID, members := range membersByDomain {
		for _, server := range members {
			if server.Role != "server" {
				continue
			}
			serverIfaces := s.interfacesForNodeLocked(server.NodeID, server.Interface)
			for _, client := range members {
				if client.NodeID == server.NodeID || client.Role != "client" {
					continue
				}
				clientIfaces := s.interfacesForNodeLocked(client.NodeID, client.Interface)
				for _, serverIface := range serverIfaces {
					if serverIface.PublicKey == "" {
						continue
					}
					for _, clientIface := range clientIfaces {
						if !containsString(clientIface.Peers, serverIface.PublicKey) {
							continue
						}
						b := Binding{
							ID:              autoBindingID(domainID, server.NodeID, serverIface.Name, client.NodeID, clientIface.Name),
							DomainID:        domainID,
							Auto:            true,
							ServerNodeID:    server.NodeID,
							ServerInterface: serverIface.Name,
							ClientNodeID:    client.NodeID,
							ClientInterface: clientIface.Name,
							PeerPublicKey:   serverIface.PublicKey,
							ConfigType:      firstNonEmpty(client.ConfigType, clientIface.ConfigType),
							ConfigPath:      clientIface.ConfigPath,
							ReloadMethod:    client.ReloadMethod,
							Enabled:         true,
						}
						validAuto[b.ID] = true
						defaultBindingFields(&b)
						if existing, ok := s.data.Bindings[b.ID]; ok {
							if existing.Auto {
								if !bindingsEqual(existing, b) {
									s.data.Bindings[b.ID] = b
									updated++
									s.addAuditEventLocked(Event{
										Type: "binding.auto_updated", Severity: "info", Actor: "daemon", Target: "binding:" + b.ID, Action: "binding.auto_update",
										Before: bindingAudit(existing), After: bindingAudit(b), Result: "success", BindingID: b.ID,
										Message: fmt.Sprintf("Auto-updated binding %s in domain %s from current domain membership and WireGuard inventory", b.ID, domainID),
										Payload: map[string]any{"domain_id": domainID},
									})
								}
								continue
							}
							continue
						}
						if s.bindingExistsLocked(domainID, server.NodeID, serverIface.Name, client.NodeID, clientIface.Name, serverIface.PublicKey) {
							continue
						}
						s.data.Bindings[b.ID] = b
						s.addAuditEventLocked(Event{
							Type: "binding.auto_created", Severity: "info", Actor: "daemon", Target: "binding:" + b.ID, Action: "binding.auto_create",
							After: bindingAudit(b), Result: "success", BindingID: b.ID,
							Message: fmt.Sprintf("Auto-created binding %s in domain %s: %s/%s -> %s/%s because client peer contains server public key %s", b.ID, domainID, b.ServerNodeID, b.ServerInterface, b.ClientNodeID, b.ClientInterface, shortKey(b.PeerPublicKey)),
							Payload: map[string]any{
								"domain_id":        domainID,
								"server_node_id":   b.ServerNodeID,
								"server_interface": b.ServerInterface,
								"client_node_id":   b.ClientNodeID,
								"client_interface": b.ClientInterface,
								"peer_public_key":  b.PeerPublicKey,
							},
						})
						created = append(created, b)
					}
				}
			}
		}
	}
	removed := 0
	for id, binding := range s.data.Bindings {
		if !binding.Auto && !isAutoBindingID(binding) {
			continue
		}
		if validAuto[id] {
			continue
		}
		delete(s.data.Bindings, id)
		delete(s.runtime.BindingEndpoint, id)
		removed++
		s.addAuditEventLocked(Event{
			Type: "binding.auto_deleted", Severity: "info", Actor: "daemon", Target: "binding:" + id, Action: "binding.auto_delete",
			Before: bindingAudit(binding), Result: "success", BindingID: id,
			Message: fmt.Sprintf("Auto-deleted binding %s because it no longer matches current domain membership and WireGuard inventory", id),
			Payload: map[string]any{"domain_id": binding.DomainID},
		})
	}
	if len(created) == 0 && updated == 0 && removed == 0 {
		return nil, nil
	}
	if err := s.saveLocked(); err != nil {
		return nil, err
	}
	return created, s.saveRuntimeLocked()
}

func (s *Store) QueueCommand(nodeID string, cmd protocol.Command) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runtime.Commands[nodeID] = append(s.runtime.Commands[nodeID], QueuedCommand{Command: cmd, Status: "pending", CreatedAt: protocol.NowISO()})
	s.addAuditEventLocked(Event{
		Type: "command.queued", Severity: "info", Actor: "daemon", Target: "node:" + nodeID, Action: "command.queue",
		After:  map[string]any{"command_id": cmd.CommandID, "action": cmd.Action, "payload": cmd.Payload, "status": "pending"},
		Result: "success", NodeID: nodeID, Message: fmt.Sprintf("Queued command %s action=%s for node %s", cmd.CommandID, cmd.Action, nodeID),
		Payload: map[string]any{"action": cmd.Action, "command_id": cmd.CommandID, "payload": cmd.Payload},
	})
	return s.saveRuntimeLocked()
}

func (s *Store) NextCommand(nodeID string) (*protocol.Command, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	queue := s.runtime.Commands[nodeID]
	for i := range queue {
		if queue[i].Status == "pending" {
			queue[i].Status = "delivered"
			s.runtime.Commands[nodeID] = queue
			cmd := queue[i].Command
			return &cmd, s.saveRuntimeLocked()
		}
	}
	return nil, nil
}

func (s *Store) CompleteCommand(nodeID, commandID string, result map[string]any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	queue := s.runtime.Commands[nodeID]
	for i := range queue {
		if queue[i].Command.CommandID == commandID {
			queue[i].Status = "completed"
			queue[i].Result = result
			break
		}
	}
	s.runtime.Commands[nodeID] = queue
	status := "completed"
	if ok, exists := result["ok"]; exists && ok == false {
		status = "failed"
	}
	severity := "info"
	errorText := ""
	if status == "failed" {
		severity = "error"
		errorText, _ = result["error"].(string)
	}
	s.addAuditEventLocked(Event{
		Type: "command.completed", Severity: severity, Actor: "agent:" + nodeID, Target: "command:" + commandID, Action: "command.complete",
		After:  map[string]any{"command_id": commandID, "status": status, "result": result},
		Result: status, Error: errorText, NodeID: nodeID, Message: fmt.Sprintf("Command %s %s on node %s: %s", commandID, status, nodeID, summarizeResult(result)),
		Payload: result,
	})
	return s.saveRuntimeLocked()
}

func (s *Store) SaveEndpointLease(nodeID string, lease EndpointLease) ([]Binding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	lease.ServerNodeID = nodeID
	lease.CreatedAt = protocol.NowISO()
	s.runtime.EndpointLeases = append(s.runtime.EndpointLeases, lease)
	var bindings []Binding
	for id, b := range s.data.Bindings {
		if b.Enabled && b.ServerNodeID == nodeID && b.ServerInterface == lease.ServerInterface {
			s.runtime.BindingEndpoint[id] = BindingEndpoint{Host: lease.PublicIP, Port: lease.PublicPort, UpdatedAt: lease.CreatedAt}
			bindings = append(bindings, s.bindingWithEndpointLocked(id, b))
		}
	}
	s.addAuditEventLocked(Event{
		Type: "endpoint.updated", Severity: "info", Actor: "agent:" + nodeID, Target: "interface:" + nodeID + "/" + lease.ServerInterface, Action: "endpoint.update",
		After:  map[string]any{"public_ip": lease.PublicIP, "public_port": lease.PublicPort, "matched_bindings": len(bindings)},
		Result: "success", NodeID: nodeID, Message: fmt.Sprintf("Node %s updated %s endpoint to %s:%d and matched %d binding(s)", nodeID, lease.ServerInterface, lease.PublicIP, lease.PublicPort, len(bindings)),
		Payload: map[string]any{
			"server_node_id":   nodeID,
			"server_interface": lease.ServerInterface,
			"public_ip":        lease.PublicIP,
			"public_port":      lease.PublicPort,
			"matched_bindings": len(bindings),
		},
	})
	return bindings, s.saveRuntimeLocked()
}

func (s *Store) Nodes() []Node {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Node, 0, len(s.data.Nodes))
	for _, n := range s.data.Nodes {
		n = s.nodeWithRuntime(n)
		n.TokenHash = ""
		clearNodeConfigFields(&n)
		n.Status = displayStatus(n)
		out = append(out, n)
	}
	return out
}

func displayStatus(n Node) string {
	if n.LastSeenAt == "" {
		return "offline"
	}
	lastSeen, err := time.Parse(time.RFC3339, n.LastSeenAt)
	if err != nil {
		return n.Status
	}
	if time.Since(lastSeen) > 90*time.Second {
		return "offline"
	}
	return n.Status
}

func (s *Store) Bindings() []Binding {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Binding, 0, len(s.data.Bindings))
	for id, b := range s.data.Bindings {
		out = append(out, s.bindingWithEndpointLocked(id, b))
	}
	return out
}

func (s *Store) Events(limit int) []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readEventsLocked(limit)
}

func (s *Store) AddEvent(eventType, severity, nodeID, bindingID, message string, payload map[string]any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.addEventLocked(eventType, severity, nodeID, bindingID, message, payload)
	return nil
}

func (s *Store) interfacesForNodeLocked(nodeID, preferredName string) []WGInterface {
	var out []WGInterface
	for _, item := range s.runtime.WGInterfaces {
		if item.NodeID != nodeID {
			continue
		}
		if preferredName != "" && item.Name != preferredName {
			continue
		}
		out = append(out, item)
	}
	return out
}

func (s *Store) bindingExistsLocked(domainID, serverNodeID, serverInterface, clientNodeID, clientInterface, peerPublicKey string) bool {
	for _, b := range s.data.Bindings {
		if b.DomainID == domainID &&
			b.ServerNodeID == serverNodeID &&
			b.ServerInterface == serverInterface &&
			b.ClientNodeID == clientNodeID &&
			b.ClientInterface == clientInterface &&
			b.PeerPublicKey == peerPublicKey {
			return true
		}
	}
	return false
}

func defaultBindingFields(b *Binding) {
	if b.ConfigType == "" {
		b.ConfigType = "openwrt_uci"
	}
	if b.ReloadMethod == "" {
		b.ReloadMethod = "none"
	}
}

func defaultMemberRuntimeFields(member *DomainMember) {
	switch member.NodeType {
	case "openwrt":
		if member.ConfigType == "" {
			member.ConfigType = "openwrt_uci"
		}
		if member.ReloadMethod == "" {
			member.ReloadMethod = "ifup"
		}
	case "linux":
		if member.ConfigType == "" {
			member.ConfigType = "wg_conf"
		}
		if member.ReloadMethod == "" {
			member.ReloadMethod = "wg-quick-restart"
		}
	}
}

func autoBindingID(domainID, serverNodeID, serverInterface, clientNodeID, clientInterface string) string {
	return cleanID(domainID + "-" + serverNodeID + "-" + serverInterface + "-to-" + clientNodeID + "-" + clientInterface)
}

func domainMemberKey(domainID, nodeID string) string {
	return domainID + "/" + nodeID
}

func wgInterfaceKey(nodeID, name string) string {
	return nodeID + "/" + name
}

var cleanIDRe = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func cleanID(value string) string {
	value = cleanIDRe.ReplaceAllString(value, "-")
	value = regexp.MustCompile(`-+`).ReplaceAllString(value, "-")
	value = regexp.MustCompile(`(^-|-$)`).ReplaceAllString(value, "")
	if value == "" {
		return "binding"
	}
	return value
}

func containsString(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func singleMemberDomainID(members map[string]DomainMember, nodeID string) string {
	var domainID string
	for _, member := range members {
		if member.NodeID != nodeID {
			continue
		}
		if domainID != "" && domainID != member.DomainID {
			return ""
		}
		domainID = member.DomainID
	}
	return domainID
}

func (s *Store) addEventLocked(eventType, severity, nodeID, bindingID, message string, payload map[string]any) {
	event := Event{
		Type: eventType, Severity: severity, NodeID: nodeID, BindingID: bindingID,
		Message: message, Payload: payload, CreatedAt: protocol.NowISO(),
	}
	switch eventType {
	case "peer.unreachable":
		event.Actor = "agent:" + nodeID
		event.Target = "binding:" + bindingID
		event.Action = "peer.unreachable"
		event.After = map[string]any{
			"binding_id":       bindingID,
			"server_node_id":   payload["server_node_id"],
			"server_interface": payload["server_interface"],
			"client_interface": payload["interface"],
			"last_handshake":   payload["last_handshake"],
		}
		event.Result = "warning"
	case "peer.recovered":
		event.Actor = "agent:" + nodeID
		event.Target = "binding:" + bindingID
		event.Action = "peer.recovered"
		event.After = map[string]any{
			"binding_id":       bindingID,
			"client_interface": payload["interface"],
			"last_handshake":   payload["last_handshake"],
		}
	case "agent.report":
		event.Actor = "agent:" + nodeID
		event.Target = "node:" + nodeID
		event.Action = "agent.report"
		event.After = payload
	default:
		if nodeID != "" {
			event.Actor = "agent:" + nodeID
			event.Target = "node:" + nodeID
		} else {
			event.Actor = "daemon"
			event.Target = eventType
		}
		if bindingID != "" {
			event.Target = "binding:" + bindingID
		}
		event.Action = eventType
		event.After = payload
	}
	s.addAuditEventLocked(event)
}

func (s *Store) addAuditEventLocked(event Event) {
	if event.CreatedAt == "" {
		event.CreatedAt = protocol.NowISO()
	}
	if event.Result == "" {
		if event.Error != "" || event.Severity == "error" {
			event.Result = "failed"
		} else {
			event.Result = "success"
		}
	}
	if err := s.appendEventFileLocked(event); err != nil {
		fmt.Fprintf(os.Stderr, "wgnh event log write failed: %v\n", err)
	}
}

func domainMemberAudit(member DomainMember) map[string]any {
	if member.DomainID == "" && member.NodeID == "" {
		return nil
	}
	return map[string]any{
		"domain_id":         member.DomainID,
		"node_id":           member.NodeID,
		"role":              member.Role,
		"node_type":         member.NodeType,
		"interface":         member.Interface,
		"config_type":       member.ConfigType,
		"reload_method":     member.ReloadMethod,
		"natter_managed":    member.NatterManaged,
		"natter_command":    member.NatterCommand,
		"natter_stop_wg":    member.NatterStopWireGuard,
		"natter_timeout":    member.NatterTimeoutSeconds,
		"natter_control":    member.NatterWireGuardControl,
		"natter_delay":      member.NatterRestartDelaySeconds,
		"natter_configured": member.NatterConfigured,
	}
}

func bindingAudit(binding Binding) map[string]any {
	return map[string]any{
		"id":               binding.ID,
		"domain_id":        binding.DomainID,
		"auto":             binding.Auto,
		"server_node_id":   binding.ServerNodeID,
		"server_interface": binding.ServerInterface,
		"client_node_id":   binding.ClientNodeID,
		"client_interface": binding.ClientInterface,
		"peer_public_key":  binding.PeerPublicKey,
		"config_type":      binding.ConfigType,
		"reload_method":    binding.ReloadMethod,
		"enabled":          binding.Enabled,
	}
}

func bindingsEqual(a, b Binding) bool {
	return a.ID == b.ID &&
		a.DomainID == b.DomainID &&
		a.Auto == b.Auto &&
		a.ServerNodeID == b.ServerNodeID &&
		a.ServerInterface == b.ServerInterface &&
		a.ClientNodeID == b.ClientNodeID &&
		a.ClientInterface == b.ClientInterface &&
		a.PeerPublicKey == b.PeerPublicKey &&
		a.ConfigType == b.ConfigType &&
		a.ConfigPath == b.ConfigPath &&
		a.ReloadMethod == b.ReloadMethod &&
		a.Enabled == b.Enabled
}

func isAutoBindingID(binding Binding) bool {
	if binding.DomainID == "" || binding.ServerNodeID == "" || binding.ServerInterface == "" || binding.ClientNodeID == "" || binding.ClientInterface == "" {
		return false
	}
	return binding.ID == autoBindingID(binding.DomainID, binding.ServerNodeID, binding.ServerInterface, binding.ClientNodeID, binding.ClientInterface)
}

func (s *Store) nodeWithRuntime(node Node) Node {
	runtime := s.runtime.NodeRuntime[node.ID]
	node.Status = runtime.Status
	node.Platform = runtime.Platform
	node.AgentVersion = runtime.AgentVersion
	node.LastSeenAt = runtime.LastSeenAt
	return node
}

func (s *Store) bindingWithEndpointLocked(id string, binding Binding) Binding {
	endpoint := s.runtime.BindingEndpoint[id]
	binding.EndpointHost = endpoint.Host
	binding.EndpointPort = endpoint.Port
	return binding
}

func clearNodeTransientFields(node *Node) {
	clearNodeConfigFields(node)
	node.Status = ""
	node.Platform = ""
	node.AgentVersion = ""
	node.LastSeenAt = ""
}

func clearNodeConfigFields(node *Node) {
	node.Role = ""
	node.DomainID = ""
	node.NodeType = ""
	node.Interface = ""
	node.ConfigType = ""
	node.ReloadMethod = ""
	node.NatterManaged = false
	node.NatterConfigured = false
	node.NatterCommand = nil
	node.NatterTimeoutSeconds = 0
	node.NatterStopWireGuard = false
	node.NatterWireGuardControl = ""
	node.NatterRestartDelaySeconds = 0
}

func (s *Store) appendEventFileLocked(event Event) error {
	if err := os.MkdirAll(filepath.Dir(s.eventPath), 0o755); err != nil && filepath.Dir(s.eventPath) != "." {
		return err
	}
	raw, err := json.Marshal(event)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(s.eventPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(raw, '\n')); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return rotateFileTail(s.eventPath, maxEventLogBytes)
}

func (s *Store) readEventsLocked(limit int) []Event {
	f, err := os.Open(s.eventPath)
	if err != nil {
		return nil
	}
	defer f.Close()
	var events []Event
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var event Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err == nil {
			events = append(events, event)
		}
	}
	if limit <= 0 || limit > len(events) {
		limit = len(events)
	}
	out := make([]Event, 0, limit)
	for i := len(events) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, events[i])
	}
	return out
}

func rotateFileTail(path string, maxBytes int64) error {
	info, err := os.Stat(path)
	if err != nil || info.Size() <= maxBytes {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Seek(-maxBytes, io.SeekEnd); err != nil {
		return err
	}
	raw, err := io.ReadAll(f)
	if err != nil {
		return err
	}
	if idx := bytes.IndexByte(raw, '\n'); idx >= 0 && idx+1 < len(raw) {
		raw = raw[idx+1:]
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func label(name, id string) string {
	if name != "" && name != id {
		return name + " (" + id + ")"
	}
	return id
}

func shortKey(value string) string {
	if len(value) <= 14 {
		return value
	}
	return value[:7] + "..." + value[len(value)-6:]
}

func summarizeResult(result map[string]any) string {
	if len(result) == 0 {
		return "no result payload"
	}
	if message, ok := result["message"].(string); ok && message != "" {
		return message
	}
	if errText, ok := result["error"].(string); ok && errText != "" {
		return errText
	}
	if host, ok := result["endpoint_host"].(string); ok && host != "" {
		return fmt.Sprintf("endpoint=%s:%v", host, result["endpoint_port"])
	}
	return fmt.Sprintf("%v", result)
}
