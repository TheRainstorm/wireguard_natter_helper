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
