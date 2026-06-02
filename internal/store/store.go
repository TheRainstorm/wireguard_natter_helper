package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

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
	Domains        map[string]Domain          `json:"domains"`
	Bindings       map[string]Binding         `json:"bindings"`
	WGInterfaces   map[string]WGInterface     `json:"wireguard_interfaces"`
	EndpointLeases []EndpointLease            `json:"endpoint_leases"`
	Commands       map[string][]QueuedCommand `json:"commands"`
	Events         []Event                    `json:"events"`
}

type Node struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Role         string `json:"role"`
	TokenHash    string `json:"token_hash"`
	DomainID     string `json:"domain_id"`
	Approved     bool   `json:"approved"`
	NodeType     string `json:"node_type"`
	Interface    string `json:"interface"`
	ConfigType   string `json:"config_type"`
	ReloadMethod string `json:"reload_method"`
	Status       string `json:"status"`
	Platform     string `json:"platform"`
	AgentVersion string `json:"agent_version"`
	LastSeenAt   string `json:"last_seen_at"`
}

type Domain struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	JoinCode    string `json:"join_code,omitempty"`
	CreatedAt   string `json:"created_at"`
	Description string `json:"description,omitempty"`
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
		Nodes:        map[string]Node{},
		Domains:      map[string]Domain{},
		Bindings:     map[string]Binding{},
		WGInterfaces: map[string]WGInterface{},
		Commands:     map[string][]QueuedCommand{},
		Events:       []Event{},
	}
}

func (s *Store) ensureMaps() {
	if s.data.Nodes == nil {
		s.data.Nodes = map[string]Node{}
	}
	if s.data.Domains == nil {
		s.data.Domains = map[string]Domain{}
	}
	if s.data.Bindings == nil {
		s.data.Bindings = map[string]Binding{}
	}
	if s.data.WGInterfaces == nil {
		s.data.WGInterfaces = map[string]WGInterface{}
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
	s.data.Nodes[id] = Node{ID: id, Name: name, Role: role, TokenHash: auth.HashToken(token), Approved: true, Status: "offline"}
	s.addEventLocked("node.upserted", "info", id, "", "Node saved", nil)
	return s.saveLocked()
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
	if node.Approved {
		node.Status = "online"
	} else {
		node.Status = "pending"
	}
	node.LastSeenAt = protocol.NowISO()
	node.Platform, _ = meta["platform"].(string)
	node.AgentVersion, _ = meta["agent_version"].(string)
	s.data.Nodes[id] = node
	return s.saveLocked()
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
	s.addEventLocked("domain.created", "info", "", "", "Domain created", map[string]any{"domain_id": id})
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
		if existing.DomainID == "" {
			existing.DomainID = domainID
		}
		existing.LastSeenAt = protocol.NowISO()
		if meta != nil {
			existing.Platform, _ = meta["platform"].(string)
			existing.AgentVersion, _ = meta["agent_version"].(string)
		}
		s.data.Nodes[nodeID] = existing
		return existing, false, s.saveLocked()
	}
	node := Node{
		ID:         nodeID,
		Name:       name,
		TokenHash:  auth.HashToken(token),
		DomainID:   domainID,
		Approved:   false,
		Status:     "pending",
		LastSeenAt: protocol.NowISO(),
	}
	if node.Name == "" {
		node.Name = node.ID
	}
	if meta != nil {
		node.Platform, _ = meta["platform"].(string)
		node.AgentVersion, _ = meta["agent_version"].(string)
	}
	s.data.Nodes[node.ID] = node
	s.addEventLocked("node.joined", "info", node.ID, "", "Node joined and is pending approval", map[string]any{"domain_id": domainID})
	return node, true, s.saveLocked()
}

type NodeApproval struct {
	DomainID     string
	Role         string
	NodeType     string
	Interface    string
	ConfigType   string
	ReloadMethod string
	Name         string
}

func (s *Store) ApproveNode(nodeID string, approval NodeApproval) (Node, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	node, ok := s.data.Nodes[nodeID]
	if !ok {
		return Node{}, errors.New("node not found")
	}
	if approval.DomainID != "" {
		if _, ok := s.data.Domains[approval.DomainID]; !ok {
			return Node{}, errors.New("domain not found")
		}
		node.DomainID = approval.DomainID
	}
	if node.DomainID == "" {
		return Node{}, errors.New("domain is required")
	}
	if approval.Name != "" {
		node.Name = approval.Name
	}
	if approval.Role != "" {
		node.Role = approval.Role
	}
	if approval.NodeType != "" {
		node.NodeType = approval.NodeType
	}
	if approval.Interface != "" {
		node.Interface = approval.Interface
	}
	if approval.ConfigType != "" {
		node.ConfigType = approval.ConfigType
	}
	if approval.ReloadMethod != "" {
		node.ReloadMethod = approval.ReloadMethod
	}
	defaultNodeRuntimeFields(&node)
	node.Approved = true
	if node.Status == "pending" {
		node.Status = "offline"
	}
	s.data.Nodes[nodeID] = node
	s.addEventLocked("node.approved", "info", node.ID, "", "Node approved", map[string]any{"domain_id": node.DomainID, "role": node.Role, "interface": node.Interface})
	return node, s.saveLocked()
}

func (s *Store) AddBinding(b Binding) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	defaultBindingFields(&b)
	b.Enabled = true
	s.data.Bindings[b.ID] = b
	s.addEventLocked("binding.upserted", "info", "", b.ID, "Binding saved", nil)
	return s.saveLocked()
}

func (s *Store) UpdateWGInterfaces(nodeID string, interfaces []WGInterface) error {
	if nodeID == "" || len(interfaces) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := protocol.NowISO()
	for key, item := range s.data.WGInterfaces {
		if item.NodeID == nodeID {
			delete(s.data.WGInterfaces, key)
		}
	}
	for _, item := range interfaces {
		if item.Name == "" {
			continue
		}
		item.NodeID = nodeID
		item.UpdatedAt = now
		s.data.WGInterfaces[wgInterfaceKey(nodeID, item.Name)] = item
	}
	return s.saveLocked()
}

func (s *Store) WGInterfaces() []WGInterface {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]WGInterface, 0, len(s.data.WGInterfaces))
	for _, item := range s.data.WGInterfaces {
		out = append(out, item)
	}
	return out
}

func (s *Store) ReconcileAutoBindings() ([]Binding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var created []Binding
	nodesByDomain := map[string][]Node{}
	for _, node := range s.data.Nodes {
		if node.Approved && node.DomainID != "" {
			nodesByDomain[node.DomainID] = append(nodesByDomain[node.DomainID], node)
		}
	}
	for domainID, nodes := range nodesByDomain {
		for _, server := range nodes {
			if server.Role != "server" {
				continue
			}
			serverIfaces := s.interfacesForNodeLocked(server.ID, server.Interface)
			for _, client := range nodes {
				if client.ID == server.ID || client.Role != "client" {
					continue
				}
				clientIfaces := s.interfacesForNodeLocked(client.ID, client.Interface)
				for _, serverIface := range serverIfaces {
					if serverIface.PublicKey == "" {
						continue
					}
					for _, clientIface := range clientIfaces {
						if !containsString(clientIface.Peers, serverIface.PublicKey) {
							continue
						}
						if s.bindingExistsLocked(server.ID, serverIface.Name, client.ID, clientIface.Name, serverIface.PublicKey) {
							continue
						}
						b := Binding{
							ID:              autoBindingID(domainID, server.ID, serverIface.Name, client.ID, clientIface.Name),
							ServerNodeID:    server.ID,
							ServerInterface: serverIface.Name,
							ClientNodeID:    client.ID,
							ClientInterface: clientIface.Name,
							PeerPublicKey:   serverIface.PublicKey,
							ConfigType:      firstNonEmpty(client.ConfigType, clientIface.ConfigType),
							ConfigPath:      clientIface.ConfigPath,
							ReloadMethod:    client.ReloadMethod,
							Enabled:         true,
						}
						defaultBindingFields(&b)
						s.data.Bindings[b.ID] = b
						s.addEventLocked("binding.auto_created", "info", "", b.ID, "Binding auto-created from WireGuard inventory", map[string]any{
							"domain_id": domainID,
						})
						created = append(created, b)
					}
				}
			}
		}
	}
	if len(created) == 0 {
		return nil, nil
	}
	return created, s.saveLocked()
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

func (s *Store) interfacesForNodeLocked(nodeID, preferredName string) []WGInterface {
	var out []WGInterface
	for _, item := range s.data.WGInterfaces {
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

func (s *Store) bindingExistsLocked(serverNodeID, serverInterface, clientNodeID, clientInterface, peerPublicKey string) bool {
	for _, b := range s.data.Bindings {
		if b.ServerNodeID == serverNodeID &&
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

func defaultNodeRuntimeFields(node *Node) {
	switch node.NodeType {
	case "openwrt":
		if node.ConfigType == "" {
			node.ConfigType = "openwrt_uci"
		}
		if node.ReloadMethod == "" {
			node.ReloadMethod = "ifup"
		}
	case "linux":
		if node.ConfigType == "" {
			node.ConfigType = "wg_conf"
		}
		if node.ReloadMethod == "" {
			node.ReloadMethod = "wg-quick-restart"
		}
	}
}

func autoBindingID(domainID, serverNodeID, serverInterface, clientNodeID, clientInterface string) string {
	return cleanID(domainID + "-" + serverNodeID + "-" + serverInterface + "-to-" + clientNodeID + "-" + clientInterface)
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

func (s *Store) addEventLocked(eventType, severity, nodeID, bindingID, message string, payload map[string]any) {
	s.data.Events = append(s.data.Events, Event{
		Type: eventType, Severity: severity, NodeID: nodeID, BindingID: bindingID,
		Message: message, Payload: payload, CreatedAt: protocol.NowISO(),
	})
}
