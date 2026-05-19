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
