package agent

import "testing"

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
