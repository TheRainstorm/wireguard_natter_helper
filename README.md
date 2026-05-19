# WireGuard Natter Helper

This repository contains a Python MVP for the design in `plan.md`.

The first implementation focuses on the Phase 1 control loop:

1. VPS daemon stores nodes, bindings, commands, endpoint leases, and events in SQLite.
2. Nodes authenticate with per-node tokens.
3. Agents poll the daemon for commands.
4. A server agent can run a configured natter command and report a UDP endpoint.
5. The daemon fans that endpoint out to matching client bindings.
6. Client agents apply the endpoint through OpenWrt UCI, Linux `wg.conf`, or `wg set`.

## Quick Start

Initialize daemon state:

```sh
python3 -m wgnh daemon init --db ./wgnh.db
python3 -m wgnh daemon create-node --db ./wgnh.db --id home-a --role server
python3 -m wgnh daemon create-node --db ./wgnh.db --id office-b --role client
```

Add a binding:

```sh
python3 -m wgnh daemon add-binding \
  --db ./wgnh.db \
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
python3 -m wgnh daemon serve --db ./wgnh.db --host 127.0.0.1 --port 8080 --admin-token '<admin-token>'
```

Run an agent:

```sh
python3 -m wgnh agent --config examples/server-agent.json
python3 -m wgnh agent --config examples/client-agent.json
```

Trigger natter manually:

```sh
curl -X POST http://127.0.0.1:8080/api/actions/run-natter \
  -H 'Content-Type: application/json' \
  -H 'Authorization: Bearer <admin-token>' \
  -d '{"server_node_id":"home-a","server_interface":"wg0"}'
```

The daemon API is intentionally minimal in this MVP. Put it behind HTTPS reverse proxy such as Caddy before using it across the internet.
