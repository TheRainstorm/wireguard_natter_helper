package wgconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpdateWGConfReplacesMatchingPeerEndpoint(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wg0.conf")
	content := `[Interface]
Address = 10.0.0.2/32

[Peer]
PublicKey = server-key
AllowedIPs = 10.0.0.1/32
Endpoint = 1.1.1.1:1111

[Peer]
PublicKey = other-key
Endpoint = 2.2.2.2:2222
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := UpdateWGConf(path, "server-key", "203.0.113.10:45182", false)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed {
		t.Fatal("expected changed result")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !strings.Contains(text, "Endpoint = 203.0.113.10:45182") {
		t.Fatalf("updated endpoint missing: %s", text)
	}
	if !strings.Contains(text, "Endpoint = 2.2.2.2:2222") {
		t.Fatalf("other endpoint should be preserved: %s", text)
	}
}

func TestUpdateWGConfInsertsMissingEndpoint(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wg0.conf")
	content := `[Interface]
Address = 10.0.0.2/32

[Peer]
PublicKey = server-key
AllowedIPs = 10.0.0.1/32
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := UpdateWGConf(path, "server-key", "203.0.113.10:45182", false)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed {
		t.Fatal("expected changed result")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "Endpoint = 203.0.113.10:45182") {
		t.Fatalf("inserted endpoint missing: %s", string(raw))
	}
}
