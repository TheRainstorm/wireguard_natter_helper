# WireGuard Natter Helper

[中文文档](README.zh-CN.md)

WireGuard Natter Helper keeps WireGuard clients pointed at a server whose UDP endpoint is created by NAT traversal and may change over time.

It is designed for this topology:

- A public VPS runs `wgnh daemon` as the control plane.
- A NAT-side WireGuard server runs `wgnh agent`, stops WireGuard when needed, runs Natter, starts WireGuard again, and reports the new public endpoint.
- Client nodes run `wgnh agent`, monitor WireGuard handshakes, and apply new endpoints when the daemon sends commands.
- An optional `wgnh web` process provides a browser dashboard for nodes, bindings, events, and manual Natter triggers.

The VPS daemon does not expose HTTP. It listens on a custom TCP JSON-line protocol, which is useful when your VPS cannot host HTTP services. The Web UI is a separate local HTTP page that talks to the daemon through the same custom TCP protocol.

## Features

- Custom TCP control protocol instead of an HTTP API on the VPS.
- Web UI domain creation and browser approval; nodes can start with only the daemon address and receive role, interface, Natter, and WireGuard settings after approval.
- Agent-reported WireGuard interface inventory with automatic binding creation.
- Node token authentication and optional admin token for management commands.
- OpenWrt UCI endpoint updates.
- Linux `wg.conf` endpoint updates.
- Automatic WireGuard stop/start around Natter mapping.
- Client monitoring with `wg show <iface> latest-handshakes`.
- Automatic `natter.run` when a client peer becomes stale.
- Browser Web UI with connection settings saved in local storage.
- Docker image support, especially for running the Web UI.

## Architecture

```text
                 custom TCP JSON-line
        +--------------------------------+
        |              VPS               |
        |          wgnh daemon            |
        +--------------------------------+
             ^            ^           ^
             |            |           |
          A agent      B agent     wgnh web
          natter       monitor     browser UI
```

Web UI traffic:

```text
browser -> wgnh web over HTTP -> custom TCP -> VPS wgnh daemon
```

## Build

```sh
go build -o wgnh ./cmd/wgnh
```

Copy the binary to the VPS, the NAT-side server, client nodes, and any machine that will run the Web UI.

## Autostart services

The repository includes ready-to-install service templates:

- `deploy/openwrt/*.init` for OpenWrt `procd`
- `deploy/systemd/*.service` and `deploy/systemd/*.env` for common Linux distributions using systemd

Install only the service you need on each machine. For example, the VPS usually runs `wgnh-daemon`, the NAT-side server and client nodes usually run `wgnh-agent`, and an admin machine can run `wgnh-web`.

### OpenWrt

For OpenWrt, install both packages from the GitHub Release or Actions artifacts:

```sh
opkg install ./wgnh_*.ipk ./luci-app-wgnh_*.ipk
/etc/init.d/uhttpd restart
```

The `wgnh` package installs the binary at `/usr/bin/wgnh`, `/etc/config/wgnh`, `/etc/init.d/wgnh-agent`, and `/etc/init.d/wgnh-daemon`. The `luci-app-wgnh` package installs only the LuCI page. Open LuCI and go to `VPN` -> `WG Natter`. Configure daemon address and admin token in `WG Natter` -> `Settings`.

In `Settings`, set `Daemon address used by LuCI status` to your VPS daemon address, for example `ecs01.yfycloud.site:3333`. Set `Admin token used by LuCI status` to the same value passed to `wgnh daemon serve --admin-token`, or leave it empty and put the token in `/etc/wgnh/admin-token`. The Status page shows remote daemon nodes, bindings, and events, plus local OpenWrt `wgnh-agent` / `wgnh-daemon` service status and recent `logread` lines.

The package sources live in `openwrt/wgnh` and `openwrt/luci-app-wgnh`. GitHub Actions builds ipk artifacts for OpenWrt 24.10.5:

- `amd64`: OpenWrt `x86/64`
- `arm64-filogic`: OpenWrt `mediatek/filogic`, package arch `aarch64_cortex-a53`, for devices such as Cudy TR3000

Tagged releases such as `v0.1.0` publish the built ipk files to GitHub Releases. Non-tag workflow runs keep the ipk files as Actions artifacts.

Check your router with `cat /etc/os-release`. Install the package whose architecture matches `OPENWRT_ARCH`; `opkg` rejects packages built for a different OpenWrt architecture.

If you install only the binary without the LuCI package, use the OpenWrt init script below. First copy the binary:

```sh
install -m 0755 ./wgnh /usr/bin/wgnh
mkdir -p /etc/wgnh
```

Install the UCI config and agent service, then set the VPS daemon address in `/etc/config/wgnh`:

```sh
cp deploy/openwrt/wgnh.config /etc/config/wgnh
cp deploy/openwrt/wgnh-agent.init /etc/init.d/wgnh-agent
chmod +x /etc/init.d/wgnh-agent

uci set wgnh.agent.daemon_addr='ecs01.yfycloud.site:3333'
uci commit wgnh

/etc/init.d/wgnh-agent enable
/etc/init.d/wgnh-agent start
```

The agent startup command now only needs the daemon address. `/etc/wgnh/node-state.json` is still generated automatically, but it only stores the local `node_id` and token; it is not the old node configuration JSON.

Check logs:

```sh
logread -f | grep wgnh
```

If this OpenWrt machine should run the Web UI:

```sh
cp deploy/openwrt/wgnh-web.init /etc/init.d/wgnh-web
chmod +x /etc/init.d/wgnh-web

uci set wgnh.web.daemon_addr='ecs01.yfycloud.site:3333'
uci set wgnh.web.admin_token='change-this-to-a-long-random-string'
uci commit wgnh

/etc/init.d/wgnh-web enable
/etc/init.d/wgnh-web start
```

If this OpenWrt machine should run the daemon, put the admin token in `/etc/wgnh/admin-token`, configure the daemon address, then enable the daemon service. `/etc/wgnh/state.json` is created automatically by the daemon:

```sh
printf '%s\n' 'change-this-to-a-long-random-string' > /etc/wgnh/admin-token
chmod 600 /etc/wgnh/admin-token

cp deploy/openwrt/wgnh-daemon.init /etc/init.d/wgnh-daemon
chmod +x /etc/init.d/wgnh-daemon

uci set wgnh.daemon.listen_addr='0.0.0.0:3333'
uci set wgnh.daemon.connect_addr='127.0.0.1:3333'
uci set wgnh.daemon.admin_token_file='/etc/wgnh/admin-token'
uci commit wgnh

/etc/init.d/wgnh-daemon enable
/etc/init.d/wgnh-daemon start
```

Edit `/etc/config/wgnh` if your binary, state path, listen address, or cooldown differs. Do not edit the init script variables.

### Linux systemd

Copy the binary:

```sh
install -m 0755 ./wgnh /usr/local/bin/wgnh
mkdir -p /etc/wgnh
```

Install and enable the agent service:

```sh
cp deploy/systemd/wgnh-agent.service /etc/systemd/system/wgnh-agent.service
cp deploy/systemd/wgnh-agent.env /etc/wgnh/agent.env
sed -i "s/your-vps.example.com:3333/ecs01.yfycloud.site:3333/" /etc/wgnh/agent.env

systemctl daemon-reload
systemctl enable --now wgnh-agent
```

`deploy/systemd/wgnh-agent.env` now only needs `WGNH_DAEMON_ADDR`; the old `agent.json` is no longer required. The agent identity state defaults to `/etc/wgnh/node-state.json` and is generated automatically; node name, role, interface, and other node configuration live in the daemon state through the Web UI.

Install and enable the daemon service:

```sh
cp deploy/systemd/wgnh-daemon.service /etc/systemd/system/wgnh-daemon.service
cp deploy/systemd/wgnh-daemon.env /etc/wgnh/daemon.env

systemctl daemon-reload
systemctl enable --now wgnh-daemon
```

Install and enable the Web UI service:

```sh
cp deploy/systemd/wgnh-web.service /etc/systemd/system/wgnh-web.service
cp deploy/systemd/wgnh-web.env /etc/wgnh/web.env

systemctl daemon-reload
systemctl enable --now wgnh-web
```

Edit `/etc/wgnh/*.env` before starting if the defaults do not match your environment. Check logs with:

```sh
journalctl -u wgnh-agent -f
journalctl -u wgnh-daemon -f
journalctl -u wgnh-web -f
```

## Docker

Build the image:

```sh
docker build -t wgnh:local .
```

Run only the Web UI:

```sh
docker run --rm -p 9090:9090 wgnh:local web --addr 0.0.0.0:9090
```

Open:

```text
http://localhost:9090
```

Enter the daemon TCP address, for example `your-vps.example.com:3333`, and the admin token in the browser. The browser stores them in local storage, so the next visit reconnects automatically.

If the daemon runs on the Docker host, use the host address that is reachable from inside the container. On Linux, `--network host` is often the simplest option:

```sh
docker run --rm --network host wgnh:local web --addr 0.0.0.0:9090
```

You can also run the daemon in Docker:

```sh
mkdir -p ./data

docker run -d --name wgnh-daemon \
  -p 3333:3333 \
  -v "$PWD/data:/data" \
  wgnh:local daemon serve \
  --state /data/state.json \
  --addr 0.0.0.0:3333 \
  --admin-token 'change-this-to-a-long-random-string'
```

Before real use, follow the simplified flow below: start the VPS daemon, create a domain in the Web UI, then start each node with only the daemon address and approve it in the browser with role, domain, and interface settings. In the normal path you do not need to manually create node tokens or run `add-binding`.

## 1. Start the daemon on the VPS

```sh
ADMIN_TOKEN='change-this-to-a-long-random-string'

./wgnh daemon init --state /etc/wgnh/state.json

./wgnh daemon serve \
  --state /etc/wgnh/state.json \
  --addr 0.0.0.0:3333 \
  --admin-token "$ADMIN_TOKEN" \
  --natter-cooldown 5m
```

Restrict `3333/tcp` with firewall rules. The current TCP protocol is plaintext.

## 2. Open the Web UI and create a domain

Run locally:

```sh
./wgnh web --addr 127.0.0.1:9090
```

Or run it in Docker:

```sh
docker run --rm -p 9090:9090 wgnh:local web --addr 0.0.0.0:9090
```

Open `http://127.0.0.1:9090`, enter:

- daemon TCP address: `your-vps.example.com:3333`
- admin token: the value passed to `wgnh daemon serve --admin-token`

After connecting, create a domain such as `home` in the `Domain` section. New nodes only need to start with the daemon address, then you assign them to a domain in the browser. `join_code` is still available for advanced setups where you want to pre-limit a node to one domain. The browser stores the daemon address and admin token in local storage.

## 3. Configure the NAT-side server agent

Start the agent on `home-a` with only the daemon address:

```sh
./wgnh agent --daemon-addr your-vps.example.com:3333
```

On first start, the agent generates a local `node_id` and token in the default `/etc/wgnh/node-state.json`, registers with the VPS as a pending node, and tries to discover WireGuard interfaces automatically. Go back to the Web UI node table, approve the node, choose the domain, choose role `server`, set interface `wg0`, and select the real node type, `openwrt` or `linux`.

Node name, role, interface, Natter command, and other settings saved in the browser are stored in the VPS daemon `--state` file, such as `/etc/wgnh/state.json` or the path passed when starting the daemon. The agent-local `/etc/wgnh/node-state.json` only stores the generated `node_id` and token; it does not store Web UI node configuration.

For server nodes, also fill the Natter command in the approval row, for example:

```text
python3 /opt/Natter/natter.py -u -i pppoe-wan -b 51820 --map-only
```

If Natter must bind the same local port as WireGuard, enable `Stop WG` and choose the control method: OpenWrt usually uses `ifup`, while Linux usually uses `wg-quick` or `systemd`.

## 4. Configure client agents

Start the agent on `office-b` the same way:

```sh
./wgnh agent --daemon-addr your-vps.example.com:3333
```

The agent generates its local identity, discovers WireGuard interfaces, and enables client monitoring after approval in the browser. Go back to the Web UI, approve the node, choose the domain, choose role `client`, and set interface `wg0`. The node type selected in the browser decides the default config behavior: OpenWrt uses `openwrt_uci/ifup`, and Linux uses `wg_conf/wg-quick-restart`.

Approved nodes remain editable in the Web UI. Change the node name, role, interface, or config fields and click `Save config`; the daemon writes the change to its state file and sends it to the agent on the next poll.

## 5. Automatic binding creation

After the server and client are both approved, the daemon creates bindings automatically from the WireGuard inventory reported by agents. The match rule is: inside the same domain, a `server` node WireGuard public key appears in a `client` node peer list.

In the Web UI, check:

- `WireGuard auto discovery`: whether nodes reported interfaces, public keys, and peers.
- `WireGuard bindings`: whether the binding was created.
- `Recent events`: look for `binding.auto_created` or errors.

If no binding appears, the client `[Peer] PublicKey` is usually not the server public key, or the agent system cannot run `wg show`.

## 6. Endpoint update flow

1. The client agent runs `wg show wg0 latest-handshakes`.
2. A stale peer is reported to the daemon after `fail_threshold` failures.
3. The daemon finds the automatic binding.
4. The daemon sends `natter.run` to the server node.
5. The server agent stops WireGuard, runs Natter, starts WireGuard, and reports the new public endpoint.
6. The daemon sends `endpoint.apply` to the client node.
7. The client agent updates the local WireGuard endpoint and reloads the interface as configured.

## 7. Trigger Natter from CLI

```sh
./wgnh daemon run-natter \
  --addr your-vps.example.com:3333 \
  --admin-token "$ADMIN_TOKEN" \
  --server-node home-a \
  --server-interface wg0
```

## 8. Query state from CLI

```sh
./wgnh daemon domains --addr your-vps.example.com:3333 --admin-token "$ADMIN_TOKEN"
./wgnh daemon nodes --addr your-vps.example.com:3333 --admin-token "$ADMIN_TOKEN"
./wgnh daemon wireguard --addr your-vps.example.com:3333 --admin-token "$ADMIN_TOKEN"
./wgnh daemon bindings --addr your-vps.example.com:3333 --admin-token "$ADMIN_TOKEN"
./wgnh daemon events --addr your-vps.example.com:3333 --admin-token "$ADMIN_TOKEN"
```

## Manual add-binding is still available

If automatic binding does not fit your topology, you can still create a binding manually:

```sh
./wgnh daemon add-binding \
  --state /etc/wgnh/state.json \
  --id home-a-wg0-office-b \
  --server-node home-a \
  --server-interface wg0 \
  --client-node office-b \
  --client-interface wg0 \
  --peer-public-key 'A_SERVER_PUBLIC_KEY' \
  --config-type openwrt_uci \
  --reload-method ifup
```

`--peer-public-key` is the server peer public key inside the client config, not the client's own key.

## Troubleshooting

### `invalid node credentials`

Check that the agent `state_path` file still exists; the default is `/etc/wgnh/node-state.json`, and it stores the generated `node_id` and token. If that file is deleted, the agent creates a new identity and must be approved again in the Web UI. Also confirm the daemon is using the expected `--state` file.

### `connection reset by peer`

The TCP connection was closed by the daemon or network. Common causes are daemon restart, firewall policy, NAT timeout, or an interrupted long poll. The agent retries automatically.

### Web UI cannot connect

The Web UI container or host must be able to open a TCP connection to the daemon address. Use a real VPS address, Docker `--network host`, or another address reachable from inside the container.

## Current limits

- The custom TCP protocol is plaintext.
- State is stored in a JSON file.
- Automatic monitoring currently uses client-side handshake freshness and a simple cooldown.
