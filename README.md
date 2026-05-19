# WireGuard Natter Helper

中文 | [English](#english)

## 中文

WireGuard Natter Helper 用来解决这个场景：

- A 是 NAT 后面的 WireGuard server。
- B/C/D 是连接 A 的 WireGuard client。
- A 的公网 UDP endpoint 会因为 NAT 映射变化而改变。
- 不想再通过 SSH 免密登录每台客户端去改 endpoint。

本项目提供一个中心 daemon 和多个 agent：

- VPS 上运行 `wgnh daemon`，作为控制面，只监听自定义 TCP JSON-line 协议，不提供 HTTP API。
- A 上运行 `wgnh agent`，负责在需要时停止 WireGuard、调用 natter 获取公网映射、再启动 WireGuard，并把 endpoint 上报给 daemon。
- B/C/D 上运行 `wgnh agent`，负责监控 WireGuard handshake 状态，并在收到新 endpoint 后更新本机配置。
- 管理机器上可以运行 `wgnh web`，提供本地 Web UI。浏览器访问的是本机 Web UI，Web UI 再通过自定义 TCP 协议连接 VPS daemon。

当前已支持：

- 自定义 TCP 控制协议，避免 VPS 暴露 HTTP 服务。
- 节点 token 认证。
- OpenWrt UCI endpoint 更新。
- Linux `wg.conf` endpoint 更新。
- 运行 natter 前自动停止 A 的 WireGuard 接口，完成后自动启动。
- client agent 基于 `wg show <iface> latest-handshakes` 自动监控断线。
- daemon 根据 binding 自动下发监控目标，client 不需要重复配置 peer 列表。
- 失联后自动触发 A 重新运行 natter。
- 本地 Web UI 查看节点、binding、endpoint、事件，并手动触发 natter。

## 架构

```text
                 custom TCP JSON-line
        +--------------------------------+
        |              VPS               |
        |          wgnh daemon            |
        +--------------------------------+
             ^            ^           ^
             |            |           |
          A agent      B agent     local wgnh web
          natter       monitor      browser UI
```

VPS daemon 不是 HTTP 服务。`wgnh web` 是单独的本地管理界面，可以跑在你的电脑上：

```text
browser -> local wgnh web -> custom TCP -> VPS wgnh daemon
```

## 编译

```sh
go build -o wgnh ./cmd/wgnh
```

把生成的 `wgnh` 放到 VPS、A、B/C/D 和管理机器上。

## 1. 在 VPS 初始化 daemon

```sh
./wgnh daemon init --state /etc/wgnh/state.json
```

创建 A 节点：

```sh
./wgnh daemon create-node \
  --state /etc/wgnh/state.json \
  --id home-a \
  --role server
```

保存输出的 token，后面填到 A 的 agent 配置里。

创建 B 节点：

```sh
./wgnh daemon create-node \
  --state /etc/wgnh/state.json \
  --id office-b \
  --role client
```

保存输出的 token，后面填到 B 的 agent 配置里。

## 2. 添加 binding

binding 表示“哪个客户端 peer 需要跟随哪个 server endpoint”。

OpenWrt 客户端示例：

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

`--peer-public-key` 填的是 B 配置里 `[Peer] PublicKey` 对应的 key，也就是 A 的 WireGuard public key，不是 B 自己的 public key。

普通 Linux 客户端示例：

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

## 3. 启动 VPS daemon

```sh
ADMIN_TOKEN='change-this-to-a-long-random-string'

./wgnh daemon serve \
  --state /etc/wgnh/state.json \
  --addr 0.0.0.0:3333 \
  --admin-token "$ADMIN_TOKEN" \
  --natter-cooldown 5m
```

建议用防火墙限制 `3333/tcp` 的访问来源。当前 TCP 协议是明文，后续可以增加 TLS 或消息加密。

## 4. 配置 A agent

A 是 NAT 后的 WireGuard server。示例 `/etc/wgnh/agent.json`：

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

`stop_wireguard: true` 很重要：natter 需要绑定 WireGuard 正在使用的本地端口，所以 agent 会先停止 `wg0`，拿到映射后再启动 `wg0`。

`wireguard_control_method` 可选：

- `ifup` / `openwrt`：`ifdown wg0` / `ifup wg0`
- `wg-quick`：`wg-quick down wg0` / `wg-quick up wg0`
- `systemd`：`systemctl stop wg-quick@wg0` / `systemctl start wg-quick@wg0`

启动：

```sh
./wgnh agent --config /etc/wgnh/agent.json
```

## 5. 配置 B/C/D agent

B 是连接 A 的客户端。示例 `/etc/wgnh/agent.json`：

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

第一次测试建议先设置 `"dry_run": true`，确认流程正常后再改成 `false`。

监控逻辑：

- client agent 定期执行 `wg show wg0 latest-handshakes`。
- 如果 peer handshake 超过 `stale_seconds`，记为一次失败。
- 连续 `fail_threshold` 次失败后，上报 `peer.unreachable`。
- daemon 根据 binding 自动给 A 下发 `natter.run`。
- 同一个 server/interface 受 `--natter-cooldown` 限制，避免频繁重启 WireGuard。

监控目标由 daemon 根据 binding 自动下发，client 配置里不需要重复写 peer 列表。

启动：

```sh
./wgnh agent --config /etc/wgnh/agent.json
```

## 6. 手动触发 natter

```sh
./wgnh daemon run-natter \
  --addr your-vps.example.com:3333 \
  --admin-token "$ADMIN_TOKEN" \
  --server-node home-a \
  --server-interface wg0
```

执行流程：

1. daemon 给 A 下发 `natter.run`。
2. A agent 停止 `wg0`。
3. A agent 执行 natter，输出 JSON endpoint。
4. A agent 启动 `wg0`。
5. A agent 上报 endpoint。
6. daemon 找到相关 binding。
7. daemon 给 B/C/D 下发 `endpoint.apply`。
8. client agent 更新本机 WireGuard endpoint 并重载接口。

## 7. 本地 Web UI

在你的管理机器上启动：

```sh
./wgnh web \
  --addr 127.0.0.1:9090 \
  --daemon-addr your-vps.example.com:3333 \
  --admin-token "$ADMIN_TOKEN"
```

浏览器打开：

```text
http://127.0.0.1:9090
```

Web UI 支持：

- 查看节点在线状态。
- 查看 binding 和当前 endpoint。
- 查看最近事件和事件 payload。
- 手动触发某个 server/interface 的 natter。
- 15 秒自动刷新。

## 8. CLI 查看状态

```sh
./wgnh daemon nodes --addr your-vps.example.com:3333 --admin-token "$ADMIN_TOKEN"
./wgnh daemon bindings --addr your-vps.example.com:3333 --admin-token "$ADMIN_TOKEN"
./wgnh daemon events --addr your-vps.example.com:3333 --admin-token "$ADMIN_TOKEN"
```

## 9. 本地 dry-run 测试

```sh
go build -o wgnh ./cmd/wgnh

./wgnh daemon init --state ./tmp-state.json
./wgnh daemon create-node --state ./tmp-state.json --id home-a --role server
./wgnh daemon create-node --state ./tmp-state.json --id office-b --role client
./wgnh daemon add-binding \
  --state ./tmp-state.json \
  --id home-a-wg0-office-b \
  --server-node home-a \
  --server-interface wg0 \
  --client-node office-b \
  --client-interface wg0 \
  --peer-public-key 'server-key' \
  --config-type openwrt_uci \
  --reload-method none

./wgnh daemon serve --state ./tmp-state.json --addr 127.0.0.1:3333 --admin-token test-admin
```

把创建节点时输出的 token 填进：

- `examples/server-agent.json`
- `examples/client-agent.json`

分别启动：

```sh
./wgnh agent --config examples/server-agent.json
./wgnh agent --config examples/client-agent.json
```

触发：

```sh
./wgnh daemon run-natter \
  --addr 127.0.0.1:3333 \
  --admin-token test-admin \
  --server-node home-a \
  --server-interface wg0
```

`examples/client-agent.json` 默认 `"dry_run": true`，不会真的修改系统配置。

## 常见问题

### agent 报 invalid node credentials

daemon 拒绝了节点凭据。检查：

- `node_id` 是否正确。
- token 是否是当前节点最新 token。
- 是否重新执行过 `daemon create-node --id <同一个节点>`，这会轮换 token。
- daemon 是否用了正确的 `--state` 文件。

查看节点：

```sh
./wgnh daemon nodes \
  --addr your-vps.example.com:3333 \
  --admin-token "$ADMIN_TOKEN"
```

重新生成 token：

```sh
./wgnh daemon create-node --state /etc/wgnh/state.json --id home-a --role server
```

更新 agent 配置后重启 agent。

### agent 显示 connection reset by peer

TCP 连接被 daemon 或中间设备断开。常见原因是 daemon 重启、防火墙或 NAT 断开长轮询连接。agent 会自动重试。

## 当前限制

- TCP 控制协议当前是明文，建议通过防火墙限制访问来源。
- 状态存储当前是 JSON 文件，不是 SQLite。
- 自动监控当前基于 client 侧 WireGuard handshake 时间，还没有复杂的多客户端投票策略。

---

## English

WireGuard Natter Helper is built for this setup:

- A is a WireGuard server behind NAT.
- B/C/D are WireGuard clients connecting to A.
- A's public UDP endpoint may change when the NAT mapping changes.
- You do not want to SSH into every client machine to update WireGuard endpoints.

The project provides one central daemon and multiple agents:

- `wgnh daemon` runs on a public IPv4 VPS. It is the control plane and listens on a custom TCP JSON-line protocol, not HTTP.
- `wgnh agent` runs on A. It can stop WireGuard, run natter, restart WireGuard, and report the new endpoint.
- `wgnh agent` runs on B/C/D. It monitors WireGuard handshake health and applies new endpoints locally.
- `wgnh web` runs on your local admin machine. Your browser talks to the local Web UI, and the Web UI talks to the VPS daemon over the custom TCP protocol.

Implemented features:

- Custom TCP control protocol; no HTTP API on the VPS.
- Per-node token authentication.
- Endpoint updates for OpenWrt UCI.
- Endpoint updates for Linux `wg.conf`.
- Stop/start WireGuard around natter mapping on the server node.
- Client-side monitoring based on `wg show <iface> latest-handshakes`.
- Monitor targets are derived from daemon bindings, so clients do not duplicate peer lists.
- Automatic `natter.run` when a peer becomes unreachable.
- Local Web UI for nodes, bindings, endpoints, events, payloads, and manual natter triggers.

## Architecture

```text
                 custom TCP JSON-line
        +--------------------------------+
        |              VPS               |
        |          wgnh daemon            |
        +--------------------------------+
             ^            ^           ^
             |            |           |
          A agent      B agent     local wgnh web
          natter       monitor      browser UI
```

The VPS daemon is not an HTTP server. The local Web UI is separate:

```text
browser -> local wgnh web -> custom TCP -> VPS wgnh daemon
```

## Build

```sh
go build -o wgnh ./cmd/wgnh
```

Copy the `wgnh` binary to the VPS, A, B/C/D, and your admin machine.

## 1. Initialize the daemon on the VPS

```sh
./wgnh daemon init --state /etc/wgnh/state.json
```

Create server node A:

```sh
./wgnh daemon create-node \
  --state /etc/wgnh/state.json \
  --id home-a \
  --role server
```

Save the printed token for A's agent config.

Create client node B:

```sh
./wgnh daemon create-node \
  --state /etc/wgnh/state.json \
  --id office-b \
  --role client
```

Save the printed token for B's agent config.

## 2. Add a binding

A binding tells the daemon which client peer should follow which server endpoint.

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

`--peer-public-key` is the `[Peer] PublicKey` in B's WireGuard config, which is A's WireGuard public key. It is not B's own public key.

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

## 3. Start the VPS daemon

```sh
ADMIN_TOKEN='change-this-to-a-long-random-string'

./wgnh daemon serve \
  --state /etc/wgnh/state.json \
  --addr 0.0.0.0:3333 \
  --admin-token "$ADMIN_TOKEN" \
  --natter-cooldown 5m
```

Use firewall rules to restrict access to `3333/tcp`. The TCP protocol is currently plaintext.

## 4. Configure agent A

Example `/etc/wgnh/agent.json` on A:

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

`stop_wireguard: true` lets the agent free the WireGuard listen port before running natter, then start WireGuard again after the mapping is obtained.

Supported `wireguard_control_method` values:

- `ifup` / `openwrt`: `ifdown wg0` / `ifup wg0`
- `wg-quick`: `wg-quick down wg0` / `wg-quick up wg0`
- `systemd`: `systemctl stop wg-quick@wg0` / `systemctl start wg-quick@wg0`

Start the agent:

```sh
./wgnh agent --config /etc/wgnh/agent.json
```

## 5. Configure client agents

Example `/etc/wgnh/agent.json` on B:

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

Use `"dry_run": true` for the first test if you want to verify the flow without changing local config.

Monitoring behavior:

- The client agent periodically runs `wg show wg0 latest-handshakes`.
- A stale handshake is counted as one failure.
- After `fail_threshold` consecutive failures, the client reports `peer.unreachable`.
- The daemon queues `natter.run` for the matching server/interface.
- `--natter-cooldown` prevents repeated WireGuard restarts.

The daemon sends monitor targets from bindings during polling, so clients do not need to repeat peer lists.

Start the agent:

```sh
./wgnh agent --config /etc/wgnh/agent.json
```

## 6. Trigger natter manually

```sh
./wgnh daemon run-natter \
  --addr your-vps.example.com:3333 \
  --admin-token "$ADMIN_TOKEN" \
  --server-node home-a \
  --server-interface wg0
```

Flow:

1. daemon sends `natter.run` to A.
2. A agent stops `wg0`.
3. A agent runs natter and gets a JSON endpoint.
4. A agent starts `wg0`.
5. A agent reports the endpoint.
6. daemon finds related bindings.
7. daemon sends `endpoint.apply` to B/C/D.
8. client agents update local WireGuard endpoint and reload the interface.

## 7. Local Web UI

Run this on your admin machine:

```sh
./wgnh web \
  --addr 127.0.0.1:9090 \
  --daemon-addr your-vps.example.com:3333 \
  --admin-token "$ADMIN_TOKEN"
```

Open:

```text
http://127.0.0.1:9090
```

The Web UI supports:

- Node status.
- Bindings and current endpoints.
- Recent events and payloads.
- Manual natter trigger per server/interface.
- 15-second auto refresh.

## 8. CLI status

```sh
./wgnh daemon nodes --addr your-vps.example.com:3333 --admin-token "$ADMIN_TOKEN"
./wgnh daemon bindings --addr your-vps.example.com:3333 --admin-token "$ADMIN_TOKEN"
./wgnh daemon events --addr your-vps.example.com:3333 --admin-token "$ADMIN_TOKEN"
```

## 9. Local dry-run test

```sh
go build -o wgnh ./cmd/wgnh

./wgnh daemon init --state ./tmp-state.json
./wgnh daemon create-node --state ./tmp-state.json --id home-a --role server
./wgnh daemon create-node --state ./tmp-state.json --id office-b --role client
./wgnh daemon add-binding \
  --state ./tmp-state.json \
  --id home-a-wg0-office-b \
  --server-node home-a \
  --server-interface wg0 \
  --client-node office-b \
  --client-interface wg0 \
  --peer-public-key 'server-key' \
  --config-type openwrt_uci \
  --reload-method none

./wgnh daemon serve --state ./tmp-state.json --addr 127.0.0.1:3333 --admin-token test-admin
```

Put the generated tokens into:

- `examples/server-agent.json`
- `examples/client-agent.json`

Start agents:

```sh
./wgnh agent --config examples/server-agent.json
./wgnh agent --config examples/client-agent.json
```

Trigger:

```sh
./wgnh daemon run-natter \
  --addr 127.0.0.1:3333 \
  --admin-token test-admin \
  --server-node home-a \
  --server-interface wg0
```

`examples/client-agent.json` uses `"dry_run": true`, so it will not modify system config.

## Troubleshooting

### `invalid node credentials`

The daemon rejected the node credentials. Check:

- `node_id`
- token
- whether `create-node` was run again for the same node
- whether daemon is using the expected `--state` file

List nodes:

```sh
./wgnh daemon nodes \
  --addr your-vps.example.com:3333 \
  --admin-token "$ADMIN_TOKEN"
```

Regenerate a token:

```sh
./wgnh daemon create-node --state /etc/wgnh/state.json --id home-a --role server
```

Update the agent config and restart the agent.

### `connection reset by peer`

The TCP connection was closed by the daemon or an intermediate network device. Common causes are daemon restart, firewall behavior, or NAT timeout. The agent retries automatically.

## Current limitations

- The TCP control protocol is plaintext. Restrict access with firewall rules.
- State storage is a JSON file, not SQLite.
- Automatic repair is based on client-side WireGuard handshake age and does not yet implement multi-client voting.
