# WireGuard Natter Helper

这个项目的目标是：不用 SSH 免密登录，让 NAT 后面的 WireGuard 服务器 A 通过 natter 获得新的公网 UDP 映射后，自动通知客户端 B/C/D 更新 WireGuard endpoint。

当前版本是 Go 写的 MVP，先实现最小可用链路：

- VPS 上运行 `daemon`，负责保存节点、配置关系和下发命令。
- A 机器上运行 `agent`，负责执行 natter 并把新的公网 endpoint 上报给 VPS。
- B/C/D 机器上运行 `agent`，负责接收新 endpoint，并更新本机 WireGuard 配置。

目前还没有 Web UI，也还没有自动判断 WireGuard 是否断线；当前需要手动调用 API 触发 A 运行 natter。

## 先理解三个角色

下面用一个最小例子说明：

- VPS：公网 IPv4 服务器，运行中心服务。
- A：家里或内网里的 WireGuard server，运行 natter，节点名叫 `home-a`。
- B：连接 A 的客户端，节点名叫 `office-b`。

配置关系是：

```text
B 的 wg0 连接 A 的 wg0
A 的公网 endpoint 变化后，B 要更新自己的 wg0 peer endpoint
```

## 编译

在有 Go 的机器上执行：

```sh
go build -o wgnh ./cmd/wgnh
```

得到一个 `wgnh` 二进制。后续 VPS、A、B 都需要这个二进制。

## 第一步：在 VPS 上初始化 daemon

在 VPS 上创建状态文件：

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

命令会输出类似：

```text
node_id=home-a
token=wgnh_xxxxxxxxxxxxxxxxx
```

把这个 token 记下来，后面要填到 A 的 agent 配置里。

创建 B 节点：

```sh
./wgnh daemon create-node \
  --state /etc/wgnh/state.json \
  --id office-b \
  --role client
```

同样把输出的 token 记下来，后面要填到 B 的 agent 配置里。

## 第二步：告诉 daemon，B 应该更新哪个 WireGuard peer

假设：

- A 的 WireGuard 接口叫 `wg0`。
- B 的 WireGuard 接口也叫 `wg0`。
- B 的配置里，代表 A 的 peer public key 是 `A_SERVER_PUBLIC_KEY`。
- B 是 OpenWrt，WireGuard 配置在 UCI 里。

在 VPS 上执行：

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

这里最容易填错的是 `--peer-public-key`。

它不是 B 自己的 public key，而是 B 的 WireGuard 配置里 `[Peer]` 对应的 `PublicKey`，也就是 A 的 WireGuard public key。程序会用这个 key 在 B 的配置里找到要更新 endpoint 的 peer。

如果 B 是普通 Linux，并且配置文件是 `/etc/wireguard/wg0.conf`，可以这样：

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

## 第三步：在 VPS 上启动 daemon

先准备一个管理 token，随便生成一个长随机字符串即可：

```sh
ADMIN_TOKEN='change-this-to-a-long-random-string'
```

启动 daemon：

```sh
./wgnh daemon serve \
  --state /etc/wgnh/state.json \
  --addr 0.0.0.0:8080 \
  --admin-token "$ADMIN_TOKEN"
```

真实部署时不要直接裸露 HTTP。建议用 Caddy 或 Nginx 在前面提供 HTTPS，然后 agent 连接 HTTPS 地址。

## 第四步：在 A 上配置 agent

在 A 机器上创建配置文件，例如 `/etc/wgnh/agent.json`：

```json
{
  "node_id": "home-a",
  "daemon_url": "https://你的VPS域名",
  "token": "第一步创建 home-a 时输出的 token",
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
      "/usr/local/bin/run-natter-and-print-json"
    ],
    "timeout_seconds": 90
  }
}
```

当前 MVP 要求 `natter.command` 最后一行输出 JSON，格式如下：

```json
{"protocol":"udp","local_ip":"192.168.1.2","local_port":51820,"public_ip":"1.2.3.4","public_port":45678}
```

当前 `3dparty/Natter/natter.py` 已经支持直接输出这个 JSON，不再需要额外包装脚本。A 上的 `natter.command` 可以写成：

```json
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
```

因为 natter 需要绑定 WireGuard 正在使用的本地端口，所以这里配置了：

- `stop_wireguard: true`：运行 natter 前先停止对应 WireGuard 接口。
- `wireguard_control_method: "ifup"`：使用 `ifdown wg0` / `ifup wg0` 控制接口，适合 OpenWrt。

执行顺序是：

1. A agent 收到 `natter.run`。
2. A agent 停止 `wg0`。
3. A agent 执行 `natter.command`，获取公网 endpoint。
4. A agent 重新启动 `wg0`。
5. A agent 把 endpoint 上报给 VPS。

如果 A 是普通 Linux，可以把控制方式改成：

```json
"wireguard_control_method": "wg-quick"
```

这会使用 `wg-quick down wg0` / `wg-quick up wg0`。

也可以使用：

```json
"wireguard_control_method": "systemd"
```

这会使用 `systemctl stop wg-quick@wg0` / `systemctl start wg-quick@wg0`。

项目里的 `examples/fake-natter.py` 仍可用于本地测试。

启动 A 的 agent：

```sh
./wgnh agent --config /etc/wgnh/agent.json
```

## 第五步：在 B 上配置 agent

在 B 机器上创建配置文件，例如 `/etc/wgnh/agent.json`：

```json
{
  "node_id": "office-b",
  "daemon_url": "https://你的VPS域名",
  "token": "第一步创建 office-b 时输出的 token",
  "role": "client",
  "retry_seconds": 5,
  "dry_run": false,
  "wireguard": [
    {
      "name": "wg0",
      "config_type": "openwrt_uci"
    }
  ]
}
```

第一次测试建议把 `"dry_run": true`，确认流程能跑通但不真的改配置。确认无误后再改成 `false`。

启动 B 的 agent：

```sh
./wgnh agent --config /etc/wgnh/agent.json
```

## 第六步：手动触发一次 natter

在任意能访问 VPS daemon 的机器上执行：

```sh
curl -X POST https://你的VPS域名/api/actions/run-natter \
  -H 'Content-Type: application/json' \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -d '{"server_node_id":"home-a","server_interface":"wg0"}'
```

执行后流程是：

1. VPS daemon 给 A 下发 `natter.run` 命令。
2. A agent 根据配置先停止 `wg0`。
3. A agent 执行 `natter.command`，获得新的公网 IP 和端口。
4. A agent 重新启动 `wg0`。
5. A agent 把新的公网 IP 和端口上报给 VPS。
6. VPS 找到 `home-a wg0 -> office-b wg0` 这条 binding。
7. VPS 给 B 下发 `endpoint.apply` 命令。
8. B agent 修改本机 WireGuard endpoint，并按配置重启接口。

## 查看状态

查看节点：

```sh
curl https://你的VPS域名/api/nodes \
  -H "Authorization: Bearer $ADMIN_TOKEN"
```

查看绑定关系：

```sh
curl https://你的VPS域名/api/bindings \
  -H "Authorization: Bearer $ADMIN_TOKEN"
```

查看事件：

```sh
curl https://你的VPS域名/api/events \
  -H "Authorization: Bearer $ADMIN_TOKEN"
```

## 本地试跑，不改真实配置

如果你只是想先在本机理解流程，可以用示例配置：

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
```

然后把创建节点时输出的两个 token 分别填进：

- `examples/server-agent.json`
- `examples/client-agent.json`

启动 daemon：

```sh
./wgnh daemon serve --state ./tmp-state.json --addr 127.0.0.1:8080 --admin-token test-admin
```

再开两个终端分别启动：

```sh
./wgnh agent --config examples/server-agent.json
./wgnh agent --config examples/client-agent.json
```

最后触发：

```sh
curl -X POST http://127.0.0.1:8080/api/actions/run-natter \
  -H 'Content-Type: application/json' \
  -H 'Authorization: Bearer test-admin' \
  -d '{"server_node_id":"home-a","server_interface":"wg0"}'
```

这个本地例子里，client 配置默认是 `"dry_run": true`，所以不会真的修改系统配置。

## 当前限制

- 还没有 Web UI。
- 还没有自动检测 WireGuard 断线。
- 当前 natter 集成需要包装脚本输出 JSON。
- 当前状态文件是 JSON，不是 SQLite。
- 当前通信建议放在 HTTPS 反代后面使用。
