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

For a LuCI dashboard, install the package built by GitHub Actions:

```sh
opkg install ./luci-app-wgnh_*.ipk
/etc/init.d/uhttpd restart
```

Open LuCI and go to `VPN` -> `WG Natter`. The LuCI page reads daemon status by executing `/usr/bin/wgnh daemon nodes|bindings|events` on the router, so the `wgnh` binary still needs to be installed separately. Configure daemon address and admin token in `WG Natter` -> `Settings`.

The package source lives in `openwrt/luci-app-wgnh`. GitHub Actions builds ipk artifacts for:

- `amd64`: OpenWrt `x86/64`
- `arm64`: OpenWrt `armsr/armv8`

Copy the binary and the agent config:

```sh
install -m 0755 ./wgnh /usr/bin/wgnh
mkdir -p /etc/wgnh
cp ./examples/server-agent-natter.json /etc/wgnh/agent.json
```

Install and enable the agent service:

```sh
cp deploy/openwrt/wgnh-agent.init /etc/init.d/wgnh-agent
chmod +x /etc/init.d/wgnh-agent
/etc/init.d/wgnh-agent enable
/etc/init.d/wgnh-agent start
```

Check logs:

```sh
logread -f | grep wgnh
```

If this OpenWrt machine should run the Web UI:

```sh
cp deploy/openwrt/wgnh-web.init /etc/init.d/wgnh-web
chmod +x /etc/init.d/wgnh-web
/etc/init.d/wgnh-web enable
/etc/init.d/wgnh-web start
```

If this OpenWrt machine should run the daemon, create `/etc/wgnh/state.json` first, put the admin token in `/etc/wgnh/admin-token`, then enable the daemon service:

```sh
printf '%s\n' 'change-this-to-a-long-random-string' > /etc/wgnh/admin-token
chmod 600 /etc/wgnh/admin-token

cp deploy/openwrt/wgnh-daemon.init /etc/init.d/wgnh-daemon
chmod +x /etc/init.d/wgnh-daemon
/etc/init.d/wgnh-daemon enable
/etc/init.d/wgnh-daemon start
```

Edit the variables at the top of the init script if your binary, config, state path, listen address, or cooldown differs.

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
cp ./examples/client-agent.json /etc/wgnh/agent.json

systemctl daemon-reload
systemctl enable --now wgnh-agent
```

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

Initialize the state file and create nodes before starting a real daemon, as shown below.

## 1. Initialize the daemon on the VPS

```sh
./wgnh daemon init --state /etc/wgnh/state.json
```

Create the NAT-side server node:

```sh
./wgnh daemon create-node \
  --state /etc/wgnh/state.json \
  --id home-a \
  --role server
```

Save the printed token for `home-a`.

Create a client node:

```sh
./wgnh daemon create-node \
  --state /etc/wgnh/state.json \
  --id office-b \
  --role client
```

Save the printed token for `office-b`.

## 2. Add a binding

A binding tells the daemon which client WireGuard peer should follow which server endpoint.

OpenWrt client example:

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

Linux `wg.conf` client example:

```sh
./wgnh daemon add-binding \
  --state /etc/wgnh/state.json \
  --id home-a-wg0-office-b \
  --server-node home-a \
  --server-interface wg0 \
  --client-node office-b \
  --client-interface wg0 \
  --peer-public-key 'A_SERVER_PUBLIC_KEY' \
  --config-type wg_conf \
  --config-path /etc/wireguard/wg0.conf \
  --reload-method wg-quick-restart
```

`--peer-public-key` is the server peer public key inside the client config, not the client's own key.

## 3. Start the daemon

```sh
ADMIN_TOKEN='change-this-to-a-long-random-string'

./wgnh daemon serve \
  --state /etc/wgnh/state.json \
  --addr 0.0.0.0:3333 \
  --admin-token "$ADMIN_TOKEN" \
  --natter-cooldown 5m
```

Restrict `3333/tcp` with firewall rules. The current TCP protocol is plaintext.

## 4. Configure the NAT-side server agent

Example `/etc/wgnh/agent.json` on `home-a`:

```json
{
  "node_id": "home-a",
  "daemon_addr": "your-vps.example.com:3333",
  "token": "token-for-home-a",
  "role": "server",
  "retry_seconds": 5,
  "wireguard": [
    {
      "name": "wg0",
      "listen_port": 51820,
      "config_type": "openwrt_uci"
    }
  ],
  "natter": {
    "stop_wireguard": true,
    "wireguard_control_method": "ifup",
    "command": [
      "python3",
      "/opt/Natter/natter.py",
      "-u",
      "-i",
      "pppoe-wan",
      "-b",
      "51820",
      "--map-only"
    ],
    "timeout_seconds": 90
  }
}
```

`stop_wireguard: true` is important because Natter must bind the same local port as WireGuard. The agent stops the interface, obtains the mapping, then starts the interface again.

Supported `wireguard_control_method` values:

- `ifup` or `openwrt`: `ifdown wg0` / `ifup wg0`
- `wg-quick`: `wg-quick down wg0` / `wg-quick up wg0`
- `systemd`: `systemctl stop wg-quick@wg0` / `systemctl start wg-quick@wg0`

Start the agent:

```sh
./wgnh agent --config /etc/wgnh/agent.json
```

## 5. Configure client agents

Example `/etc/wgnh/agent.json` on `office-b`:

```json
{
  "node_id": "office-b",
  "daemon_addr": "your-vps.example.com:3333",
  "token": "token-for-office-b",
  "role": "client",
  "retry_seconds": 5,
  "dry_run": false,
  "wireguard": [
    {
      "name": "wg0",
      "config_type": "openwrt_uci"
    }
  ],
  "monitor": {
    "enabled": true,
    "interval_seconds": 30,
    "stale_seconds": 180,
    "fail_threshold": 3
  }
}
```

Use `"dry_run": true` for the first test if you want to verify the flow without modifying local config.

Monitoring flow:

1. The client agent runs `wg show wg0 latest-handshakes`.
2. A stale peer is reported to the daemon after `fail_threshold` failures.
3. The daemon finds the matching binding.
4. The daemon sends `natter.run` to the server node.
5. The server reports a new endpoint.
6. The daemon sends `endpoint.apply` to the client node.

Start the agent:

```sh
./wgnh agent --config /etc/wgnh/agent.json
```

## 6. Use the Web UI

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

The browser stores both values in local storage. Any browser that can reach the Web UI can open the page, but it still needs the admin token to connect to the daemon.

The Web UI can:

- show node online status
- show bindings and current endpoints
- show recent events and payloads
- manually trigger Natter for a server/interface
- auto-refresh every 15 seconds after a successful connection

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
./wgnh daemon nodes --addr your-vps.example.com:3333 --admin-token "$ADMIN_TOKEN"
./wgnh daemon bindings --addr your-vps.example.com:3333 --admin-token "$ADMIN_TOKEN"
./wgnh daemon events --addr your-vps.example.com:3333 --admin-token "$ADMIN_TOKEN"
```

## Troubleshooting

### `invalid node credentials`

Check `node_id`, token, and the daemon `--state` file. Running `daemon create-node` again for the same node rotates the token, so the agent config must be updated.

### `connection reset by peer`

The TCP connection was closed by the daemon or network. Common causes are daemon restart, firewall policy, NAT timeout, or an interrupted long poll. The agent retries automatically.

### Web UI cannot connect

The Web UI container or host must be able to open a TCP connection to the daemon address. Use a real VPS address, Docker `--network host`, or another address reachable from inside the container.

## Current limits

- The custom TCP protocol is plaintext.
- State is stored in a JSON file.
- Automatic monitoring currently uses client-side handshake freshness and a simple cooldown.
