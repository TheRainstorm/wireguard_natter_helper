package store

import (
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
	if err := st.CreateNode("node-a", "Node A", "server", token); err != nil {
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
	node, err := st.ApproveNode("router", NodeApproval{Role: "client", NodeType: "openwrt", Interface: "wg0"})
	if err != nil {
		t.Fatal(err)
	}
	if node.ConfigType != "openwrt_uci" || node.ReloadMethod != "ifup" {
		t.Fatalf("unexpected openwrt defaults: %#v", node)
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
	if node.DomainID != "home" || !node.Approved || node.ConfigType != "wg_conf" || node.ReloadMethod != "wg-quick-restart" {
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
	node, err := st.ApproveNode("server", NodeApproval{
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
	})
	if err != nil {
		t.Fatal(err)
	}
	if !node.NatterManaged || !node.NatterConfigured || len(node.NatterCommand) != 3 || node.NatterCommand[1] != "/opt/Natter/natter.py" || node.NatterTimeoutSeconds != 60 || !node.NatterStopWireGuard || node.NatterWireGuardControl != "ifup" || node.NatterRestartDelaySeconds != 3 {
		t.Fatalf("unexpected natter config: %#v", node)
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
	if node.Name != "New Name" || node.Role != "server" || node.NodeType != "openwrt" || node.Interface != "wg1" || node.ConfigType != "openwrt_uci" || node.ReloadMethod != "ifup" {
		t.Fatalf("unexpected updated node: %#v", node)
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
	node, err := st.ApproveNode("node", NodeApproval{Role: "client", Interface: "wg0", NatterManaged: true})
	if err != nil {
		t.Fatal(err)
	}
	if node.NatterConfigured || len(node.NatterCommand) != 0 || node.NatterTimeoutSeconds != 0 || node.NatterStopWireGuard || node.NatterWireGuardControl != "" || node.NatterRestartDelaySeconds != 0 {
		t.Fatalf("expected natter config cleared: %#v", node)
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
	node, err := st.ApproveNode("node", NodeApproval{Role: "server", Interface: "wg0", NatterManaged: true, NatterConfigured: false})
	if err != nil {
		t.Fatal(err)
	}
	if node.NatterConfigured || len(node.NatterCommand) != 0 || node.NatterTimeoutSeconds != 0 || node.NatterStopWireGuard || node.NatterWireGuardControl != "" || node.NatterRestartDelaySeconds != 0 {
		t.Fatalf("expected natter config cleared: %#v", node)
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
	node, err := st.ApproveNode("node", NodeApproval{Role: "server", Interface: "wg1"})
	if err != nil {
		t.Fatal(err)
	}
	if !node.NatterManaged || !node.NatterConfigured || len(node.NatterCommand) != 2 || node.NatterCommand[1] != "natter.py" {
		t.Fatalf("expected natter config preserved: %#v", node)
	}
}
