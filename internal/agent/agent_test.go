package agent

import (
	"path/filepath"
	"testing"

	"github.com/yfy/wireguard-natter-helper/internal/store"
)

func TestParseNatterOutputUsesLastJSONLine(t *testing.T) {
	got, err := ParseNatterOutput([]byte(`
noise
{"protocol":"udp","local_ip":"192.168.1.2","local_port":51820,"public_ip":"203.0.113.10","public_port":45182}
`))
	if err != nil {
		t.Fatal(err)
	}
	if got["protocol"] != "udp" || got["public_ip"] != "203.0.113.10" {
		t.Fatalf("unexpected payload: %#v", got)
	}
}

func TestWireGuardControlMethodDefaultsByConfigType(t *testing.T) {
	a := &Agent{config: Config{
		WireGuard: []WGInterface{
			{Name: "wg0", ConfigType: "openwrt_uci"},
			{Name: "wg1", ConfigType: "wg_conf"},
		},
	}}

	got, err := a.wireGuardControlMethod("wg0")
	if err != nil {
		t.Fatal(err)
	}
	if got != "ifup" {
		t.Fatalf("expected ifup, got %s", got)
	}

	got, err = a.wireGuardControlMethod("wg1")
	if err != nil {
		t.Fatal(err)
	}
	if got != "wg-quick" {
		t.Fatalf("expected wg-quick, got %s", got)
	}
}

func TestParseLatestHandshakes(t *testing.T) {
	got, err := ParseLatestHandshakes("peer-a 1710000000\npeer-b\t0\n")
	if err != nil {
		t.Fatal(err)
	}
	if got["peer-a"] != 1710000000 {
		t.Fatalf("unexpected peer-a timestamp: %d", got["peer-a"])
	}
	if got["peer-b"] != 0 {
		t.Fatalf("unexpected peer-b timestamp: %d", got["peer-b"])
	}
}

func TestParseWhitespaceList(t *testing.T) {
	got := parseWhitespaceList("wg0 wg1\nwg2\t\n")
	want := []string{"wg0", "wg1", "wg2"}
	if len(got) != len(want) {
		t.Fatalf("unexpected length: %#v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unexpected item %d: got %q want %q", i, got[i], want[i])
		}
	}
}

func TestNewAutoCreatesLocalStateWithoutJoinCode(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "node-state.json")
	a, err := New(Config{DaemonAddr: "127.0.0.1:3333", StatePath: statePath})
	if err != nil {
		t.Fatal(err)
	}
	if a.config.NodeID == "" || a.token == "" {
		t.Fatalf("expected generated identity, node_id=%q token=%q", a.config.NodeID, a.token)
	}
}

func TestApplyRemoteNodeConfigEnablesClientMonitor(t *testing.T) {
	a := &Agent{}
	a.applyRemoteNodeConfig(store.Node{
		Role:         "client",
		Interface:    "wg0",
		ConfigType:   "openwrt_uci",
		ReloadMethod: "ifup",
	})
	if !a.config.Monitor.Enabled {
		t.Fatal("client remote config should enable monitor")
	}
	if len(a.config.WireGuard) != 1 || a.config.WireGuard[0].Name != "wg0" || a.config.WireGuard[0].ConfigType != "openwrt_uci" {
		t.Fatalf("unexpected wireguard config: %#v", a.config.WireGuard)
	}
}

func TestApplyRemoteNodeConfigAppliesNatterCommand(t *testing.T) {
	a := &Agent{}
	a.applyRemoteNodeConfig(store.Node{
		Role:                      "server",
		Interface:                 "wg0",
		NatterManaged:             true,
		NatterConfigured:          true,
		NatterCommand:             []string{"python3", "/opt/Natter/natter.py", "--map-only"},
		NatterTimeoutSeconds:      45,
		NatterStopWireGuard:       true,
		NatterWireGuardControl:    "ifup",
		NatterRestartDelaySeconds: 2,
	})
	if !sameStrings(a.config.Natter.Command, []string{"python3", "/opt/Natter/natter.py", "--map-only"}) {
		t.Fatalf("unexpected natter command: %#v", a.config.Natter.Command)
	}
	if a.config.Natter.TimeoutSeconds != 45 || !a.config.Natter.StopWireGuard || a.config.Natter.WireGuardControlMethod != "ifup" || a.config.Natter.RestartDelaySeconds != 2 {
		t.Fatalf("unexpected natter config: %#v", a.config.Natter)
	}
}

func TestApplyRemoteNodeConfigClearsNatterCommand(t *testing.T) {
	a := &Agent{config: Config{Natter: NatterConfig{
		Command:                []string{"python3", "natter.py"},
		TimeoutSeconds:         45,
		StopWireGuard:          true,
		WireGuardControlMethod: "ifup",
		RestartDelaySeconds:    2,
	}}}
	a.applyRemoteNodeConfig(store.Node{Role: "server", Interface: "wg0", NatterManaged: true})
	if len(a.config.Natter.Command) != 0 || a.config.Natter.TimeoutSeconds != 0 || a.config.Natter.StopWireGuard || a.config.Natter.WireGuardControlMethod != "" || a.config.Natter.RestartDelaySeconds != 0 {
		t.Fatalf("expected natter config cleared: %#v", a.config.Natter)
	}
}

func TestApplyRemoteNodeConfigPreservesLocalNatterWhenUnmanaged(t *testing.T) {
	a := &Agent{config: Config{Natter: NatterConfig{Command: []string{"python3", "natter.py"}}}}
	a.applyRemoteNodeConfig(store.Node{Role: "server", Interface: "wg0"})
	if !sameStrings(a.config.Natter.Command, []string{"python3", "natter.py"}) {
		t.Fatalf("expected local natter preserved: %#v", a.config.Natter)
	}
}
