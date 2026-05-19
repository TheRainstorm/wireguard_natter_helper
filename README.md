# WireGuard Natter Helper

Go MVP for the design in `plan.md`.

The current implementation focuses on the Phase 1 control loop:

1. VPS daemon stores nodes, bindings, commands, endpoint leases, and events in a local JSON state file.
2. Nodes authenticate with per-node bearer tokens.
3. Agents long-poll the daemon for commands.
4. A server agent runs a configured natter command and reports a UDP endpoint.
5. The daemon fans that endpoint out to matching client bindings.
6. Client agents apply the endpoint through OpenWrt UCI, Linux `wg.conf`, or `wg set`.

The JSON state backend is intentionally dependency-free and cross-compile friendly. A SQLite backend can be added later behind the same store boundary.

## Build

```sh
go build -o wgnh ./cmd/wgnh
```

## Quick Start

Initialize daemon state:

```sh
./wgnh daemon init --state ./wgnh-state.json
./wgnh daemon create-node --state ./wgnh-state.json --id home-a --role server
./wgnh daemon create-node --state ./wgnh-state.json --id office-b --role client
```

Add a binding:

```sh
./wgnh daemon add-binding \
  --state ./wgnh-state.json \
  --id home-a-wg0-office-b \
  --server-node home-a \
  --server-interface wg0 \
  --client-node office-b \
  --client-interface wg0 \
  --peer-public-key '<server-peer-public-key>' \
  --config-type openwrt_uci \
  --reload-method ifup
```

Start the daemon:

```sh
./wgnh daemon serve --state ./wgnh-state.json --addr 127.0.0.1:8080 --admin-token '<admin-token>'
```

Run agents:

```sh
./wgnh agent --config examples/server-agent.json
./wgnh agent --config examples/client-agent.json
```

Trigger natter manually:

```sh
curl -X POST http://127.0.0.1:8080/api/actions/run-natter \
  -H 'Content-Type: application/json' \
  -H 'Authorization: Bearer <admin-token>' \
  -d '{"server_node_id":"home-a","server_interface":"wg0"}'
```

Put the daemon behind HTTPS, such as Caddy or Nginx, before using it across the internet.
