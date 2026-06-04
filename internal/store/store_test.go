package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yfy/wireguard-natter-helper/internal/auth"
	"github.com/yfy/wireguard-natter-helper/internal/protocol"
)

func TestStoreAuthAndCommandQueue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	token, err := auth.GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreateNode("node-a", "Node A", token); err != nil {
		t.Fatal(err)
	}
	if _, ok := st.AuthenticateNode("node-a", token); !ok {
		t.Fatal("expected valid token")
	}
	if _, ok := st.AuthenticateNode("node-a", "bad"); ok {
		t.Fatal("bad token should not authenticate")
	}
	cmd := protocol.NewCommand("natter.run", map[string]any{"server_interface": "wg0"})
	if err := st.QueueCommand("node-a", cmd); err != nil {
		t.Fatal(err)
	}
	got, err := st.NextCommand("node-a")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.CommandID != cmd.CommandID || got.Action != "natter.run" {
		t.Fatalf("unexpected command: %#v", got)
	}
	nodes := st.Nodes()
	if len(nodes) != 1 || nodes[0].TokenHash != "" {
		t.Fatalf("expected public nodes to hide token hash: %#v", nodes)
	}
}

func TestDisplayStatusMarksStaleNodeOffline(t *testing.T) {
	node := Node{
		Status:     "online",
		LastSeenAt: time.Now().Add(-2 * time.Minute).UTC().Format(time.RFC3339),
	}
	if got := displayStatus(node); got != "offline" {
		t.Fatalf("expected offline, got %s", got)
	}
}

func TestReconcileAutoBindingsCreatesClientEndpointBinding(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreateDomain("home", "Home", "join-home", ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.UpsertJoinedNode("home", "server-a", "Server A", "server-token", nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.UpsertJoinedNode("home", "client-b", "Client B", "client-token", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApproveNode("server-a", NodeApproval{Role: "server", Interface: "wg0", ConfigType: "wg_conf", ReloadMethod: "wg-quick-restart"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApproveNode("client-b", NodeApproval{Role: "client", Interface: "wg0", ConfigType: "openwrt_uci", ReloadMethod: "ifup"}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateWGInterfaces("server-a", []WGInterface{{
		Name:      "wg0",
		PublicKey: "server-public-key",
	}}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateWGInterfaces("client-b", []WGInterface{{
		Name:       "wg0",
		PublicKey:  "client-public-key",
		Peers:      []string{"server-public-key"},
		ConfigType: "openwrt_uci",
	}}); err != nil {
		t.Fatal(err)
	}

	created, err := st.ReconcileAutoBindings()
	if err != nil {
		t.Fatal(err)
	}
	if len(created) != 1 {
		t.Fatalf("expected one binding, got %#v", created)
	}
	binding := created[0]
	if binding.ServerNodeID != "server-a" || binding.ClientNodeID != "client-b" || binding.PeerPublicKey != "server-public-key" {
		t.Fatalf("unexpected binding: %#v", binding)
	}
	if binding.ConfigType != "openwrt_uci" || binding.ReloadMethod != "ifup" {
		t.Fatalf("unexpected client config fields: %#v", binding)
	}

	created, err = st.ReconcileAutoBindings()
	if err != nil {
		t.Fatal(err)
	}
	if len(created) != 0 {
		t.Fatalf("expected idempotent reconcile, got %#v", created)
	}
}

func TestReconcileAutoBindingsUsesDomainMembershipInterface(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreateDomain("wg0-domain", "WG0", "join-wg0", ""); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateDomain("wg1-domain", "WG1", "join-wg1", ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.UpsertPendingNode("op1", "op1", "op1-token", nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.UpsertPendingNode("mi4a", "mi4a", "mi4a-token", nil); err != nil {
		t.Fatal(err)
	}
	for _, item := range []NodeApproval{
		{DomainID: "wg0-domain", Role: "server", Interface: "wg0", ConfigType: "openwrt_uci", ReloadMethod: "ifup"},
		{DomainID: "wg1-domain", Role: "server", Interface: "wg1", ConfigType: "openwrt_uci", ReloadMethod: "ifup"},
	} {
		if _, err := st.ApproveNode("op1", item); err != nil {
			t.Fatal(err)
		}
	}
	for _, item := range []NodeApproval{
		{DomainID: "wg0-domain", Role: "client", Interface: "wg0", ConfigType: "openwrt_uci", ReloadMethod: "ifup"},
		{DomainID: "wg1-domain", Role: "client", Interface: "wg1", ConfigType: "openwrt_uci", ReloadMethod: "ifup"},
	} {
		if _, err := st.ApproveNode("mi4a", item); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.UpdateWGInterfaces("op1", []WGInterface{
		{Name: "wg0", PublicKey: "op1-shared-key"},
		{Name: "wg1", PublicKey: "op1-shared-key"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateWGInterfaces("mi4a", []WGInterface{
		{Name: "wg0", PublicKey: "mi4a-shared-key", Peers: []string{"op1-shared-key"}},
		{Name: "wg1", PublicKey: "mi4a-shared-key", Peers: []string{"op1-shared-key"}},
	}); err != nil {
		t.Fatal(err)
	}

	created, err := st.ReconcileAutoBindings()
	if err != nil {
		t.Fatal(err)
	}
	if len(created) != 2 {
		t.Fatalf("expected two domain-specific bindings, got %#v", created)
	}
	got := map[string]Binding{}
	for _, binding := range created {
		got[binding.DomainID] = binding
	}
	if got["wg0-domain"].ServerInterface != "wg0" || got["wg0-domain"].ClientInterface != "wg0" {
		t.Fatalf("unexpected wg0 binding: %#v", got["wg0-domain"])
	}
	if got["wg1-domain"].ServerInterface != "wg1" || got["wg1-domain"].ClientInterface != "wg1" {
		t.Fatalf("unexpected wg1 binding: %#v", got["wg1-domain"])
	}
}

func TestReconcileAutoBindingsDeletesStaleAutoBinding(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreateDomain("home", "Home", "join-home", ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.UpsertPendingNode("op1", "op1", "op1-token", nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.UpsertPendingNode("mi4a", "mi4a", "mi4a-token", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApproveNode("op1", NodeApproval{DomainID: "home", Role: "server", NodeType: "openwrt", Interface: "wg0"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApproveNode("mi4a", NodeApproval{DomainID: "home", Role: "client", NodeType: "openwrt", Interface: "wg0"}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateWGInterfaces("op1", []WGInterface{
		{Name: "wg0", PublicKey: "op1-key"},
		{Name: "wg1", PublicKey: "op1-key"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateWGInterfaces("mi4a", []WGInterface{
		{Name: "wg0", PublicKey: "mi4a-key", Peers: []string{"op1-key"}},
		{Name: "wg1", PublicKey: "mi4a-key", Peers: []string{"op1-key"}},
	}); err != nil {
		t.Fatal(err)
	}

	created, err := st.ReconcileAutoBindings()
	if err != nil {
		t.Fatal(err)
	}
	if len(created) != 1 || created[0].ServerInterface != "wg0" || !created[0].Auto {
		t.Fatalf("expected initial wg0 auto binding, got %#v", created)
	}

	if _, err := st.ApproveNode("op1", NodeApproval{DomainID: "home", Role: "server", NodeType: "openwrt", Interface: "wg1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApproveNode("mi4a", NodeApproval{DomainID: "home", Role: "client", NodeType: "openwrt", Interface: "wg1"}); err != nil {
		t.Fatal(err)
	}
	created, err = st.ReconcileAutoBindings()
	if err != nil {
		t.Fatal(err)
	}
	if len(created) != 1 || created[0].ServerInterface != "wg1" || created[0].ClientInterface != "wg1" {
		t.Fatalf("expected replacement wg1 binding, got %#v", created)
	}
	bindings := st.Bindings()
	if len(bindings) != 1 || bindings[0].ServerInterface != "wg1" || bindings[0].ClientInterface != "wg1" {
		t.Fatalf("expected stale wg0 binding removed: %#v", bindings)
	}
	var sawDelete bool
	for _, event := range st.Events(20) {
		if event.Action == "binding.auto_delete" {
			sawDelete = true
			if event.Actor != "daemon" || event.Target == "" || event.Before["server_interface"] != "wg0" || event.Result != "success" {
				t.Fatalf("unexpected auto delete audit event: %#v", event)
			}
		}
	}
	if !sawDelete {
		t.Fatalf("expected binding.auto_delete event, got %#v", st.Events(20))
	}
}

func TestReconcileAutoBindingsKeepsAutoBindingDuringInventoryGap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreateDomain("home", "Home", "join-home", ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.UpsertPendingNode("op1", "op1", "op1-token", nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.UpsertPendingNode("mi4a", "mi4a", "mi4a-token", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApproveNode("op1", NodeApproval{DomainID: "home", Role: "server", NodeType: "openwrt", Interface: "wg1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApproveNode("mi4a", NodeApproval{DomainID: "home", Role: "client", NodeType: "openwrt", Interface: "wg1"}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateWGInterfaces("op1", []WGInterface{{Name: "wg1", PublicKey: "op1-key"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateWGInterfaces("mi4a", []WGInterface{{Name: "wg1", PublicKey: "mi4a-key", Peers: []string{"op1-key"}}}); err != nil {
		t.Fatal(err)
	}
	if created, err := st.ReconcileAutoBindings(); err != nil || len(created) != 1 {
		t.Fatalf("expected initial auto binding, created=%#v err=%v", created, err)
	}

	if err := st.UpdateWGInterfaces("mi4a", []WGInterface{{Name: "wg1", PublicKey: "mi4a-key"}}); err != nil {
		t.Fatal(err)
	}
	if created, err := st.ReconcileAutoBindings(); err != nil || len(created) != 0 {
		t.Fatalf("inventory gap should not create replacement binding, created=%#v err=%v", created, err)
	}
	bindings := st.Bindings()
	if len(bindings) != 1 || bindings[0].ServerInterface != "wg1" || bindings[0].ClientInterface != "wg1" {
		t.Fatalf("auto binding should survive inventory gap: %#v", bindings)
	}
	for _, event := range st.Events(20) {
		if event.Action == "binding.auto_delete" {
			t.Fatalf("inventory gap should not emit auto delete: %#v", event)
		}
	}
}

func TestAuditEventsIncludeStructuredChangeFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreateDomain("home", "Home", "join-home", ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.UpsertPendingNode("router", "Router", "router-token", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApproveNode("router", NodeApproval{DomainID: "home", Role: "client", NodeType: "linux", Interface: "wg0"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApproveNode("router", NodeApproval{DomainID: "home", Role: "server", NodeType: "openwrt", Interface: "wg1"}); err != nil {
		t.Fatal(err)
	}

	var event Event
	for _, item := range st.Events(20) {
		if item.Action == "membership.save" && item.Before != nil && item.Before["interface"] == "wg0" {
			event = item
			break
		}
	}
	if event.Action == "" {
		t.Fatalf("expected membership update audit event, got %#v", st.Events(20))
	}
	if event.Actor != "admin" || event.Target != "membership:home/router" || event.Result != "success" {
		t.Fatalf("unexpected audit identity fields: %#v", event)
	}
	if event.Before["role"] != "client" || event.After["role"] != "server" || event.After["interface"] != "wg1" {
		t.Fatalf("unexpected audit change fields: %#v", event)
	}
	if _, err := time.Parse(time.RFC3339, event.CreatedAt); err != nil {
		t.Fatalf("event timestamp should be RFC3339: %q", event.CreatedAt)
	}
}

func TestDeleteNodeRemovesRelatedState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreateDomain("home", "Home", "join-home", ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.UpsertJoinedNode("home", "server-a", "Server A", "server-token", nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.UpsertJoinedNode("home", "client-b", "Client B", "client-token", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApproveNode("server-a", NodeApproval{Role: "server", Interface: "wg0"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApproveNode("client-b", NodeApproval{Role: "client", Interface: "wg0"}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateWGInterfaces("server-a", []WGInterface{{Name: "wg0", PublicKey: "server-key"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddBinding(Binding{ID: "binding-1", ServerNodeID: "server-a", ServerInterface: "wg0", ClientNodeID: "client-b", ClientInterface: "wg0", PeerPublicKey: "server-key"}); err != nil {
		t.Fatal(err)
	}
	if err := st.QueueCommand("server-a", protocol.NewCommand("natter.run", nil)); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteNode("server-a"); err != nil {
		t.Fatal(err)
	}
	for _, node := range st.Nodes() {
		if node.ID == "server-a" {
			t.Fatalf("deleted node still exists: %#v", node)
		}
	}
	if len(st.Bindings()) != 0 {
		t.Fatalf("expected related bindings deleted: %#v", st.Bindings())
	}
	if len(st.WGInterfaces()) != 0 {
		t.Fatalf("expected related wg inventory deleted: %#v", st.WGInterfaces())
	}
	if _, err := st.NextCommand("server-a"); err != nil {
		t.Fatal(err)
	}
	if len(st.Nodes()) != 1 || st.Nodes()[0].ID != "client-b" {
		t.Fatalf("unrelated node should remain: %#v", st.Nodes())
	}
}

func TestApproveNodeDefaultsRuntimeFieldsByNodeType(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreateDomain("home", "Home", "join-home", ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.UpsertJoinedNode("home", "router", "Router", "router-token", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApproveNode("router", NodeApproval{Role: "client", NodeType: "openwrt", Interface: "wg0"}); err != nil {
		t.Fatal(err)
	}
	member := memberFor(t, st, "home", "router")
	if member.ConfigType != "openwrt_uci" || member.ReloadMethod != "ifup" {
		t.Fatalf("unexpected openwrt defaults: %#v", member)
	}
}

func TestNodeDoesNotStoreDomainMemberConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreateDomain("home", "Home", "join-home", ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.UpsertPendingNode("router", "Router", "router-token", nil); err != nil {
		t.Fatal(err)
	}
	node, err := st.ApproveNode("router", NodeApproval{DomainID: "home", Role: "server", NodeType: "openwrt", Interface: "wg0"})
	if err != nil {
		t.Fatal(err)
	}
	if node.Role != "" || node.DomainID != "" || node.Interface != "" || node.ConfigType != "" || node.ReloadMethod != "" {
		t.Fatalf("node should not carry domain member config: %#v", node)
	}
}

func TestStateFileOmitsRuntimeAndNodeMemberFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreateDomain("home", "Home", "join-home", ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.UpsertPendingNode("router", "Router", "router-token", map[string]any{"platform": "linux/amd64"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApproveNode("router", NodeApproval{DomainID: "home", Role: "server", NodeType: "openwrt", Interface: "wg0"}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateWGInterfaces("router", []WGInterface{{Name: "wg0", PublicKey: "router-key"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.QueueCommand("router", protocol.NewCommand("natter.run", map[string]any{"server_interface": "wg0"})); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"events", "wireguard_interfaces", "commands", "endpoint_leases"} {
		if _, exists := payload[key]; exists {
			t.Fatalf("state file should not contain %s: %s", key, string(raw))
		}
	}
	nodes, ok := payload["nodes"].(map[string]any)
	if !ok {
		t.Fatalf("missing nodes: %s", string(raw))
	}
	router, ok := nodes["router"].(map[string]any)
	if !ok {
		t.Fatalf("missing router: %s", string(raw))
	}
	for _, key := range []string{"role", "domain_id", "interface", "config_type", "reload_method", "status", "platform", "last_seen_at"} {
		if _, exists := router[key]; exists {
			t.Fatalf("node should not contain %s: %#v", key, router)
		}
	}
	if _, err := os.Stat(path + ".runtime.json"); err != nil {
		t.Fatalf("expected runtime file: %v", err)
	}
	if _, err := os.Stat(path + ".events.jsonl"); err != nil {
		t.Fatalf("expected event log file: %v", err)
	}
}

func TestPendingNodeCanRegisterWithoutDomainThenBeApproved(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreateDomain("home", "Home", "join-home", ""); err != nil {
		t.Fatal(err)
	}
	node, created, err := st.UpsertPendingNode("node-auto", "Auto Node", "auto-token", map[string]any{"platform": "linux/amd64"})
	if err != nil {
		t.Fatal(err)
	}
	if !created || node.DomainID != "" || node.Approved {
		t.Fatalf("unexpected pending node: created=%t node=%#v", created, node)
	}
	if _, ok := st.AuthenticateNode("node-auto", "auto-token"); ok {
		t.Fatal("pending node should not authenticate as approved")
	}
	node, err = st.ApproveNode("node-auto", NodeApproval{DomainID: "home", Role: "client", NodeType: "linux", Interface: "wg0"})
	if err != nil {
		t.Fatal(err)
	}
	member := memberFor(t, st, "home", "node-auto")
	if !node.Approved || node.DomainID != "" || member.ConfigType != "wg_conf" || member.ReloadMethod != "wg-quick-restart" {
		t.Fatalf("unexpected approved node: %#v", node)
	}
	if _, ok := st.AuthenticateNode("node-auto", "auto-token"); !ok {
		t.Fatal("approved node should authenticate")
	}
}

func TestApproveNodeStoresNatterConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreateDomain("home", "Home", "join-home", ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.UpsertPendingNode("server", "Server", "server-token", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApproveNode("server", NodeApproval{
		DomainID:                  "home",
		Role:                      "server",
		NodeType:                  "openwrt",
		Interface:                 "wg0",
		NatterManaged:             true,
		NatterConfigured:          true,
		NatterCommand:             []string{"python3", "/opt/Natter/natter.py", "--map-only"},
		NatterTimeoutSeconds:      60,
		NatterStopWireGuard:       true,
		NatterWireGuardControl:    "ifup",
		NatterRestartDelaySeconds: 3,
	}); err != nil {
		t.Fatal(err)
	}
	member := memberFor(t, st, "home", "server")
	if !member.NatterManaged || !member.NatterConfigured || len(member.NatterCommand) != 3 || member.NatterCommand[1] != "/opt/Natter/natter.py" || member.NatterTimeoutSeconds != 60 || !member.NatterStopWireGuard || member.NatterWireGuardControl != "ifup" || member.NatterRestartDelaySeconds != 3 {
		t.Fatalf("unexpected natter config: %#v", member)
	}
}

func TestApproveNodeUpdatesApprovedNodeConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreateDomain("home", "Home", "join-home", ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.UpsertPendingNode("node", "Old Name", "node-token", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApproveNode("node", NodeApproval{DomainID: "home", Role: "client", NodeType: "linux", Interface: "wg0"}); err != nil {
		t.Fatal(err)
	}
	node, err := st.ApproveNode("node", NodeApproval{Name: "New Name", DomainID: "home", Role: "server", NodeType: "openwrt", Interface: "wg1", ConfigType: "openwrt_uci", ReloadMethod: "ifup"})
	if err != nil {
		t.Fatal(err)
	}
	member := memberFor(t, st, "home", "node")
	if node.Name != "New Name" || member.Role != "server" || member.NodeType != "openwrt" || member.Interface != "wg1" || member.ConfigType != "openwrt_uci" || member.ReloadMethod != "ifup" {
		t.Fatalf("unexpected updated member: node=%#v member=%#v", node, member)
	}
}

func TestApproveNodeClearsNatterWhenChangedToClient(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreateDomain("home", "Home", "join-home", ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.UpsertPendingNode("node", "Node", "node-token", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApproveNode("node", NodeApproval{
		DomainID:                  "home",
		Role:                      "server",
		Interface:                 "wg0",
		NatterManaged:             true,
		NatterConfigured:          true,
		NatterCommand:             []string{"python3", "natter.py"},
		NatterTimeoutSeconds:      60,
		NatterStopWireGuard:       true,
		NatterWireGuardControl:    "ifup",
		NatterRestartDelaySeconds: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApproveNode("node", NodeApproval{Role: "client", Interface: "wg0", NatterManaged: true}); err != nil {
		t.Fatal(err)
	}
	member := memberFor(t, st, "home", "node")
	if member.NatterConfigured || len(member.NatterCommand) != 0 || member.NatterTimeoutSeconds != 0 || member.NatterStopWireGuard || member.NatterWireGuardControl != "" || member.NatterRestartDelaySeconds != 0 {
		t.Fatalf("expected natter config cleared: %#v", member)
	}
}

func TestApproveNodeClearsNatterWhenServerCommandRemoved(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreateDomain("home", "Home", "join-home", ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.UpsertPendingNode("node", "Node", "node-token", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApproveNode("node", NodeApproval{
		DomainID:                  "home",
		Role:                      "server",
		Interface:                 "wg0",
		NatterManaged:             true,
		NatterConfigured:          true,
		NatterCommand:             []string{"python3", "natter.py"},
		NatterTimeoutSeconds:      60,
		NatterStopWireGuard:       true,
		NatterWireGuardControl:    "ifup",
		NatterRestartDelaySeconds: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApproveNode("node", NodeApproval{Role: "server", Interface: "wg0", NatterManaged: true, NatterConfigured: false}); err != nil {
		t.Fatal(err)
	}
	member := memberFor(t, st, "home", "node")
	if member.NatterConfigured || len(member.NatterCommand) != 0 || member.NatterTimeoutSeconds != 0 || member.NatterStopWireGuard || member.NatterWireGuardControl != "" || member.NatterRestartDelaySeconds != 0 {
		t.Fatalf("expected natter config cleared: %#v", member)
	}
}

func TestApproveNodeWithoutNatterManagedPreservesNatter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreateDomain("home", "Home", "join-home", ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.UpsertPendingNode("node", "Node", "node-token", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApproveNode("node", NodeApproval{
		DomainID:                  "home",
		Role:                      "server",
		Interface:                 "wg0",
		NatterManaged:             true,
		NatterConfigured:          true,
		NatterCommand:             []string{"python3", "natter.py"},
		NatterTimeoutSeconds:      60,
		NatterStopWireGuard:       true,
		NatterWireGuardControl:    "ifup",
		NatterRestartDelaySeconds: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApproveNode("node", NodeApproval{Role: "server", Interface: "wg1"}); err != nil {
		t.Fatal(err)
	}
	member := memberFor(t, st, "home", "node")
	if !member.NatterManaged || !member.NatterConfigured || len(member.NatterCommand) != 2 || member.NatterCommand[1] != "natter.py" {
		t.Fatalf("expected natter config preserved: %#v", member)
	}
}

func memberFor(t *testing.T, st *Store, domainID, nodeID string) DomainMember {
	t.Helper()
	for _, member := range st.DomainMembers() {
		if member.DomainID == domainID && member.NodeID == nodeID {
			return member
		}
	}
	t.Fatalf("member %s/%s not found: %#v", domainID, nodeID, st.DomainMembers())
	return DomainMember{}
}
