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
