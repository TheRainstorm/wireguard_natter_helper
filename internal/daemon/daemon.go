package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/yfy/wireguard-natter-helper/internal/auth"
	"github.com/yfy/wireguard-natter-helper/internal/protocol"
	"github.com/yfy/wireguard-natter-helper/internal/store"
)

type Server struct {
	store      *store.Store
	adminToken string
	pollWait   time.Duration
}

func New(st *store.Store, adminToken string) *Server {
	return &Server{store: st, adminToken: adminToken, pollWait: 25 * time.Second}
}

func (s *Server) ListenAndServe(addr string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.health)
	mux.HandleFunc("/agent/poll", s.agentPoll)
	mux.HandleFunc("/agent/report", s.agentReport)
	mux.HandleFunc("/api/nodes", s.apiNodes)
	mux.HandleFunc("/api/bindings", s.apiBindings)
	mux.HandleFunc("/api/events", s.apiEvents)
	mux.HandleFunc("/api/actions/run-natter", s.apiRunNatter)
	return http.ListenAndServe(addr, mux)
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) agentPoll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	nodeID, ok := s.authenticateAgent(w, r)
	if !ok {
		return
	}
	var body struct {
		Meta map[string]any `json:"meta"`
	}
	if err := readJSON(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	_ = s.store.MarkSeen(nodeID, body.Meta)

	var cmd *protocol.Command
	deadline := time.Now().Add(s.pollWait)
	for time.Now().Before(deadline) {
		next, err := s.store.NextCommand(nodeID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		if next != nil {
			cmd = next
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "command": cmd})
}

func (s *Server) agentReport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	nodeID, ok := s.authenticateAgent(w, r)
	if !ok {
		return
	}
	var body struct {
		Type      string         `json:"type"`
		CommandID string         `json:"command_id"`
		Payload   map[string]any `json:"payload"`
	}
	if err := readJSON(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}

	switch body.Type {
	case "action.result":
		if err := s.store.CompleteCommand(nodeID, body.CommandID, body.Payload); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	case "natter.result":
		lease, err := leaseFromPayload(body.Payload)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		bindings, err := s.store.SaveEndpointLease(nodeID, lease)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		for _, b := range bindings {
			cmd := protocol.NewCommand("endpoint.apply", map[string]any{
				"binding_id":      b.ID,
				"interface":       b.ClientInterface,
				"peer_public_key": b.PeerPublicKey,
				"config_type":     b.ConfigType,
				"config_path":     b.ConfigPath,
				"reload_method":   b.ReloadMethod,
				"endpoint_host":   lease.PublicIP,
				"endpoint_port":   lease.PublicPort,
			})
			if err := s.store.QueueCommand(b.ClientNodeID, cmd); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
				return
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "queued": len(bindings)})
	default:
		_ = s.store.AddEvent("agent.report", "info", nodeID, "", "Agent report", body.Payload)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	}
}

func (s *Server) apiNodes(w http.ResponseWriter, r *http.Request) {
	if !s.authenticateAdmin(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"nodes": s.store.Nodes()})
}

func (s *Server) apiBindings(w http.ResponseWriter, r *http.Request) {
	if !s.authenticateAdmin(w, r) {
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"bindings": s.store.Bindings()})
	case http.MethodPost:
		var b store.Binding
		if err := readJSON(r, &b); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		if b.ID == "" || b.ServerNodeID == "" || b.ClientNodeID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "id, server_node_id and client_node_id are required"})
			return
		}
		if err := s.store.AddBinding(b); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
	}
}

func (s *Server) apiEvents(w http.ResponseWriter, r *http.Request) {
	if !s.authenticateAdmin(w, r) {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 100
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": s.store.Events(limit)})
}

func (s *Server) apiRunNatter(w http.ResponseWriter, r *http.Request) {
	if !s.authenticateAdmin(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	var body struct {
		ServerNodeID    string `json:"server_node_id"`
		ServerInterface string `json:"server_interface"`
	}
	if err := readJSON(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	cmd := protocol.NewCommand("natter.run", map[string]any{"server_interface": body.ServerInterface})
	if err := s.store.QueueCommand(body.ServerNodeID, cmd); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "command": cmd})
}

func (s *Server) authenticateAgent(w http.ResponseWriter, r *http.Request) (string, bool) {
	nodeID := r.Header.Get("X-Node-ID")
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if nodeID == "" || token == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "missing node credentials"})
		return "", false
	}
	if _, ok := s.store.AuthenticateNode(nodeID, token); !ok {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "invalid node credentials"})
		return "", false
	}
	return nodeID, true
}

func (s *Server) authenticateAdmin(w http.ResponseWriter, r *http.Request) bool {
	if s.adminToken == "" {
		return true
	}
	if r.Header.Get("Authorization") == "Bearer "+s.adminToken {
		return true
	}
	writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "invalid admin credentials"})
	return false
}

func leaseFromPayload(payload map[string]any) (store.EndpointLease, error) {
	if payload["protocol"] != "udp" {
		return store.EndpointLease{}, errors.New("WireGuard endpoint protocol must be udp")
	}
	publicPort, err := number(payload["public_port"])
	if err != nil || publicPort < 1 || publicPort > 65535 {
		return store.EndpointLease{}, errors.New("invalid public_port")
	}
	localPort, _ := number(payload["local_port"])
	lease := store.EndpointLease{
		ServerInterface: stringValue(payload["server_interface"]),
		Protocol:        "udp",
		LocalIP:         stringValue(payload["local_ip"]),
		LocalPort:       localPort,
		PublicIP:        stringValue(payload["public_ip"]),
		PublicPort:      publicPort,
	}
	if lease.ServerInterface == "" || lease.PublicIP == "" {
		return store.EndpointLease{}, errors.New("server_interface and public_ip are required")
	}
	return lease, nil
}

func stringValue(v any) string {
	s, _ := v.(string)
	return s
}

func number(v any) (int, error) {
	switch n := v.(type) {
	case float64:
		return int(n), nil
	case int:
		return n, nil
	case json.Number:
		i, err := n.Int64()
		return int(i), err
	default:
		return 0, fmt.Errorf("not a number")
	}
}

func readJSON(r *http.Request, dst any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(r.Body)
	dec.UseNumber()
	return dec.Decode(dst)
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	raw, err := json.Marshal(data)
	if err != nil {
		status = http.StatusInternalServerError
		raw = []byte(`{"error":"json encode failed"}`)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(raw)
}

func CreateNode(path, id, name, role string) (string, error) {
	token, err := auth.GenerateToken()
	if err != nil {
		return "", err
	}
	st, err := store.Open(path)
	if err != nil {
		return "", err
	}
	if name == "" {
		name = id
	}
	return token, st.CreateNode(id, name, role, token)
}
