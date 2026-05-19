package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"github.com/yfy/wireguard-natter-helper/internal/auth"
	"github.com/yfy/wireguard-natter-helper/internal/protocol"
	"github.com/yfy/wireguard-natter-helper/internal/rpc"
	"github.com/yfy/wireguard-natter-helper/internal/store"
)

type Server struct {
	store          *store.Store
	adminToken     string
	pollWait       time.Duration
	natterCooldown time.Duration
	natterMu       sync.Mutex
	lastNatterRun  map[string]time.Time
}

func New(st *store.Store, adminToken string) *Server {
	return &Server{
		store:          st,
		adminToken:     adminToken,
		pollWait:       25 * time.Second,
		natterCooldown: 5 * time.Minute,
		lastNatterRun:  map[string]time.Time{},
	}
}

func (s *Server) ListenAndServe(addr string) error {
	addr, err := rpc.NormalizeAddr(addr)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	defer listener.Close()
	for {
		conn, err := listener.Accept()
		if err != nil {
			return err
		}
		go s.handleConn(conn)
	}
}

func (s *Server) handleConn(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(s.pollWait + 10*time.Second))
	var req rpc.Request
	if err := json.NewDecoder(conn).Decode(&req); err != nil {
		s.write(conn, rpc.Response{OK: false, Error: "invalid request json: " + err.Error()})
		return
	}
	resp := s.handle(req, conn.RemoteAddr().String())
	s.write(conn, resp)
}

func (s *Server) write(conn net.Conn, resp rpc.Response) {
	if err := json.NewEncoder(conn).Encode(resp); err != nil {
		log.Printf("tcp write response failed remote=%s error=%v", conn.RemoteAddr(), err)
	}
}

func (s *Server) handle(req rpc.Request, remote string) rpc.Response {
	switch req.Kind {
	case "agent.poll":
		return s.agentPoll(req, remote)
	case "agent.report":
		return s.agentReport(req, remote)
	case "admin.nodes":
		if err := s.authenticateAdmin(req); err != nil {
			return rpc.Response{OK: false, Error: err.Error()}
		}
		return rpc.Response{OK: true, Nodes: s.store.Nodes()}
	case "admin.bindings":
		if err := s.authenticateAdmin(req); err != nil {
			return rpc.Response{OK: false, Error: err.Error()}
		}
		return rpc.Response{OK: true, Bindings: s.store.Bindings()}
	case "admin.events":
		if err := s.authenticateAdmin(req); err != nil {
			return rpc.Response{OK: false, Error: err.Error()}
		}
		limit := req.Limit
		if limit <= 0 {
			limit = 100
		}
		return rpc.Response{OK: true, Events: s.store.Events(limit)}
	case "admin.run_natter":
		return s.adminRunNatter(req)
	default:
		return rpc.Response{OK: false, Error: "unknown request kind: " + req.Kind}
	}
}

func (s *Server) agentPoll(req rpc.Request, remote string) rpc.Response {
	nodeID, err := s.authenticateAgent(req, remote)
	if err != nil {
		return rpc.Response{OK: false, Error: err.Error()}
	}
	_ = s.store.MarkSeen(nodeID, req.Meta)

	var cmd *protocol.Command
	deadline := time.Now().Add(s.pollWait)
	for time.Now().Before(deadline) {
		next, err := s.store.NextCommand(nodeID)
		if err != nil {
			return rpc.Response{OK: false, Error: err.Error()}
		}
		if next != nil {
			cmd = next
			log.Printf("delivering command node=%s id=%s action=%s", nodeID, cmd.CommandID, cmd.Action)
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	return rpc.Response{OK: true, Command: cmd}
}

func (s *Server) agentReport(req rpc.Request, remote string) rpc.Response {
	nodeID, err := s.authenticateAgent(req, remote)
	if err != nil {
		return rpc.Response{OK: false, Error: err.Error()}
	}

	switch req.ReportType {
	case "action.result":
		log.Printf("action result node=%s command=%s ok=%v", nodeID, req.CommandID, req.Payload["ok"])
		if err := s.store.CompleteCommand(nodeID, req.CommandID, req.Payload); err != nil {
			return rpc.Response{OK: false, Error: err.Error()}
		}
		return rpc.Response{OK: true}
	case "natter.result":
		lease, err := leaseFromPayload(req.Payload)
		if err != nil {
			return rpc.Response{OK: false, Error: err.Error()}
		}
		bindings, err := s.store.SaveEndpointLease(nodeID, lease)
		if err != nil {
			return rpc.Response{OK: false, Error: err.Error()}
		}
		log.Printf("natter result node=%s interface=%s public=%s:%d bindings=%d", nodeID, lease.ServerInterface, lease.PublicIP, lease.PublicPort, len(bindings))
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
				return rpc.Response{OK: false, Error: err.Error()}
			}
			log.Printf("queued endpoint.apply command node=%s binding=%s command=%s", b.ClientNodeID, b.ID, cmd.CommandID)
		}
		return rpc.Response{OK: true, Queued: len(bindings)}
	case "peer.unreachable":
		return s.peerUnreachable(nodeID, req.Payload)
	case "peer.recovered":
		_ = s.store.AddEvent("peer.recovered", "info", nodeID, stringValue(req.Payload["binding_id"]), "Peer handshake recovered", req.Payload)
		log.Printf("peer recovered node=%s binding=%s interface=%s", nodeID, stringValue(req.Payload["binding_id"]), stringValue(req.Payload["interface"]))
		return rpc.Response{OK: true}
	default:
		_ = s.store.AddEvent("agent.report", "info", nodeID, "", "Agent report", req.Payload)
		return rpc.Response{OK: true}
	}
}

func (s *Server) peerUnreachable(nodeID string, payload map[string]any) rpc.Response {
	serverNodeID := stringValue(payload["server_node_id"])
	serverInterface := stringValue(payload["server_interface"])
	bindingID := stringValue(payload["binding_id"])
	if serverNodeID == "" || serverInterface == "" {
		return rpc.Response{OK: false, Error: "peer.unreachable requires server_node_id and server_interface"}
	}
	_ = s.store.AddEvent("peer.unreachable", "warning", nodeID, bindingID, "Peer handshake is stale", payload)
	key := serverNodeID + "/" + serverInterface
	now := time.Now()
	s.natterMu.Lock()
	defer s.natterMu.Unlock()
	if last := s.lastNatterRun[key]; !last.IsZero() && now.Sub(last) < s.natterCooldown {
		log.Printf("auto natter suppressed by cooldown server=%s interface=%s binding=%s remaining=%s", serverNodeID, serverInterface, bindingID, s.natterCooldown-now.Sub(last))
		return rpc.Response{OK: true}
	}
	cmd := protocol.NewCommand("natter.run", map[string]any{"server_interface": serverInterface})
	if err := s.store.QueueCommand(serverNodeID, cmd); err != nil {
		return rpc.Response{OK: false, Error: err.Error()}
	}
	s.lastNatterRun[key] = now
	log.Printf("auto queued natter.run server=%s interface=%s binding=%s command=%s", serverNodeID, serverInterface, bindingID, cmd.CommandID)
	return rpc.Response{OK: true, Command: &cmd}
}

func (s *Server) adminRunNatter(req rpc.Request) rpc.Response {
	if err := s.authenticateAdmin(req); err != nil {
		return rpc.Response{OK: false, Error: err.Error()}
	}
	if req.ServerNodeID == "" || req.ServerInterface == "" {
		return rpc.Response{OK: false, Error: "server_node_id and server_interface are required"}
	}
	cmd := protocol.NewCommand("natter.run", map[string]any{"server_interface": req.ServerInterface})
	if err := s.store.QueueCommand(req.ServerNodeID, cmd); err != nil {
		return rpc.Response{OK: false, Error: err.Error()}
	}
	log.Printf("queued natter.run command node=%s interface=%s command=%s", req.ServerNodeID, req.ServerInterface, cmd.CommandID)
	return rpc.Response{OK: true, Command: &cmd}
}

func (s *Server) authenticateAgent(req rpc.Request, remote string) (string, error) {
	if req.NodeID == "" || req.Token == "" {
		log.Printf("agent auth failed remote=%s node=%q reason=missing credentials", remote, req.NodeID)
		return "", errors.New("missing node credentials")
	}
	if _, ok := s.store.AuthenticateNode(req.NodeID, req.Token); !ok {
		log.Printf("agent auth failed remote=%s node=%s reason=invalid token or unknown node", remote, req.NodeID)
		return "", errors.New("invalid node credentials")
	}
	return req.NodeID, nil
}

func (s *Server) authenticateAdmin(req rpc.Request) error {
	if s.adminToken == "" {
		return nil
	}
	if req.AdminToken == s.adminToken {
		return nil
	}
	return errors.New("invalid admin credentials")
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
