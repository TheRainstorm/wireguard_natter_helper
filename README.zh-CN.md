# WireGuard Natter Helper

[English](README.md)

WireGuard Natter Helper 用来维护 NAT 后 WireGuard server 的公网 UDP endpoint。当 NAT 映射变化时，它可以自动让客户端更新 endpoint，不需要你再 SSH 到每台客户端手动修改。

典型场景：

- VPS 运行 `wgnh daemon`，作为控制面。
- NAT 后的 WireGuard server 运行 `wgnh agent`，需要时停止 WireGuard、执行 Natter、重新启动 WireGuard，并把新的公网 endpoint 上报给 VPS。
- WireGuard client 运行 `wgnh agent`，监控 handshake 状态，并在收到命令时修改本机 endpoint。
- 可选运行 `wgnh web`，用浏览器查看节点、绑定、事件，并手动触发 Natter。

VPS daemon 不提供 HTTP API，只监听自定义 TCP JSON-line 协议。Web UI 是单独的本地 HTTP 页面，它再通过自定义 TCP 协议连接 VPS daemon，适合 VPS 不能搭建 HTTP 服务的网络环境。

## 功能

- VPS 使用自定义 TCP 控制协议，不暴露 HTTP API。
- 节点 token 认证，管理命令可配置 admin token。
- 支持 OpenWrt UCI endpoint 更新。
- 支持 Linux `wg.conf` endpoint 更新。
- 执行 Natter 前自动停止 WireGuard，完成后自动启动。
- client agent 基于 `wg show <iface> latest-handshakes` 监控断线。
- client 断线后自动触发 server agent 重新运行 Natter。
- Web UI 支持浏览器输入 daemon 地址和 admin token，并保存到浏览器 localStorage。
- 支持 Docker 运行，尤其适合只跑 Web UI。

## 架构

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

Web UI 流量路径：

```text
browser -> wgnh web over HTTP -> custom TCP -> VPS wgnh daemon
```

## 编译

```sh
go build -o wgnh ./cmd/wgnh
```

把生成的 `wgnh` 放到 VPS、NAT 后的 WireGuard server、客户端，以及需要运行 Web UI 的机器上。

## 自启动服务

仓库里已经提供可直接安装的服务模板：

- `deploy/openwrt/*.init`：OpenWrt `procd` 自启动脚本
- `deploy/systemd/*.service` 和 `deploy/systemd/*.env`：普通 Linux systemd 服务

每台机器只安装自己需要的服务。一般来说，VPS 跑 `wgnh-daemon`，NAT 后的 server 和客户端跑 `wgnh-agent`，管理机器可以跑 `wgnh-web`。

### OpenWrt

OpenWrt 上安装 GitHub Release 或 Actions artifacts 里的两个包：

```sh
opkg install ./wgnh_*.ipk ./luci-app-wgnh_*.ipk
/etc/init.d/uhttpd restart
```

`wgnh` 包会把二进制安装到 `/usr/bin/wgnh`。`luci-app-wgnh` 包负责安装 LuCI 页面、UCI 配置和 OpenWrt 服务脚本。打开 LuCI，进入 `VPN` -> `WG Natter`，daemon 地址和 admin token 在 `WG Natter` -> `Settings` 里配置。

在 `Settings` 里，`Daemon address used by LuCI status` 填 VPS daemon 地址，例如 `ecs01.yfycloud.site:3333`。`Admin token used by LuCI status` 填启动 `wgnh daemon serve --admin-token` 时使用的 token；也可以留空，然后把 token 放到 `/etc/wgnh/admin-token`。`Status` 页面会显示远端 daemon 的节点、binding、事件，也会显示本机 OpenWrt 上 `wgnh-agent` / `wgnh-daemon` 的服务状态和最近 `logread` 日志。

包源码在 `openwrt/wgnh` 和 `openwrt/luci-app-wgnh`。GitHub Actions 会为 OpenWrt 24.10.5 自动编译：

- `amd64`：OpenWrt `x86/64`
- `arm64-filogic`：OpenWrt `mediatek/filogic`，包架构是 `aarch64_cortex-a53`，适合 Cudy TR3000 这类设备
- `arm64-generic`：OpenWrt `armsr/armv8`，包架构是 `aarch64_generic`

推送 `v0.1.0` 这类 tag 时，CI 会把编译好的 ipk 发布到 GitHub Releases。普通非 tag workflow run 会把 ipk 保存在 Actions artifacts。

可以用 `cat /etc/os-release` 查看路由器的 `OPENWRT_ARCH`。必须安装匹配架构的 ipk，否则 `opkg` 会提示 incompatible with the architectures configured。

先复制二进制和 agent 配置：

```sh
install -m 0755 ./wgnh /usr/bin/wgnh
mkdir -p /etc/wgnh
cp ./examples/server-agent-natter.json /etc/wgnh/agent.json
```

安装并启用 agent 服务：

```sh
cp deploy/openwrt/wgnh-agent.init /etc/init.d/wgnh-agent
chmod +x /etc/init.d/wgnh-agent
/etc/init.d/wgnh-agent enable
/etc/init.d/wgnh-agent start
```

查看日志：

```sh
logread -f | grep wgnh
```

如果这台 OpenWrt 需要跑 Web UI：

```sh
cp deploy/openwrt/wgnh-web.init /etc/init.d/wgnh-web
chmod +x /etc/init.d/wgnh-web
/etc/init.d/wgnh-web enable
/etc/init.d/wgnh-web start
```

如果这台 OpenWrt 需要跑 daemon，先创建 `/etc/wgnh/state.json`，把 admin token 放到 `/etc/wgnh/admin-token`，再启用 daemon 服务：

```sh
printf '%s\n' 'change-this-to-a-long-random-string' > /etc/wgnh/admin-token
chmod 600 /etc/wgnh/admin-token

cp deploy/openwrt/wgnh-daemon.init /etc/init.d/wgnh-daemon
chmod +x /etc/init.d/wgnh-daemon
/etc/init.d/wgnh-daemon enable
/etc/init.d/wgnh-daemon start
```

如果你的二进制、配置文件、state 路径、监听地址或 cooldown 不同，直接修改 init 脚本顶部变量。

### 普通 Linux systemd

复制二进制：

```sh
install -m 0755 ./wgnh /usr/local/bin/wgnh
mkdir -p /etc/wgnh
```

安装并启用 agent 服务：

```sh
cp deploy/systemd/wgnh-agent.service /etc/systemd/system/wgnh-agent.service
cp deploy/systemd/wgnh-agent.env /etc/wgnh/agent.env
cp ./examples/client-agent.json /etc/wgnh/agent.json

systemctl daemon-reload
systemctl enable --now wgnh-agent
```

安装并启用 daemon 服务：

```sh
cp deploy/systemd/wgnh-daemon.service /etc/systemd/system/wgnh-daemon.service
cp deploy/systemd/wgnh-daemon.env /etc/wgnh/daemon.env

systemctl daemon-reload
systemctl enable --now wgnh-daemon
```

安装并启用 Web UI 服务：

```sh
cp deploy/systemd/wgnh-web.service /etc/systemd/system/wgnh-web.service
cp deploy/systemd/wgnh-web.env /etc/wgnh/web.env

systemctl daemon-reload
systemctl enable --now wgnh-web
```

如果默认值不符合你的环境，启动前先修改 `/etc/wgnh/*.env`。查看日志：

```sh
journalctl -u wgnh-agent -f
journalctl -u wgnh-daemon -f
journalctl -u wgnh-web -f
```

## Docker

构建镜像：

```sh
docker build -t wgnh:local .
```

只运行 Web UI：

```sh
docker run --rm -p 9090:9090 wgnh:local web --addr 0.0.0.0:9090
```

浏览器打开：

```text
http://localhost:9090
```

在网页里输入 daemon TCP 地址，例如 `your-vps.example.com:3333`，以及 admin token。浏览器会把连接信息保存到 localStorage，下次打开会自动使用。

如果 daemon 跑在 Docker 宿主机上，需要填容器内能访问到的地址。Linux 上最简单的方式通常是使用 host 网络：

```sh
docker run --rm --network host wgnh:local web --addr 0.0.0.0:9090
```

也可以用 Docker 跑 daemon：

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

真实使用前，先按下面步骤初始化 state 并创建节点。

## 1. 在 VPS 初始化 daemon

```sh
./wgnh daemon init --state /etc/wgnh/state.json
```

创建 NAT 后的 server 节点：

```sh
./wgnh daemon create-node \
  --state /etc/wgnh/state.json \
  --id home-a \
  --role server
```

保存输出的 token，后面填到 `home-a` 的 agent 配置里。

创建 client 节点：

```sh
./wgnh daemon create-node \
  --state /etc/wgnh/state.json \
  --id office-b \
  --role client
```

保存输出的 token，后面填到 `office-b` 的 agent 配置里。

## 2. 添加 binding

binding 表示“哪个客户端的 WireGuard peer 需要跟随哪个 server endpoint”。

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

普通 Linux `wg.conf` 客户端示例：

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

`--peer-public-key` 填的是客户端配置里 `[Peer] PublicKey` 对应的 server 公钥，不是客户端自己的公钥。

## 3. 启动 daemon

```sh
ADMIN_TOKEN='change-this-to-a-long-random-string'

./wgnh daemon serve \
  --state /etc/wgnh/state.json \
  --addr 0.0.0.0:3333 \
  --admin-token "$ADMIN_TOKEN" \
  --natter-cooldown 5m
```

建议用防火墙限制 `3333/tcp` 的访问来源。当前 TCP 协议是明文。

## 4. 配置 NAT 后的 server agent

`home-a` 上的 `/etc/wgnh/agent.json` 示例：

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

`stop_wireguard: true` 很重要：Natter 需要绑定 WireGuard 使用的本地端口，所以 agent 会先停止接口，拿到映射后再启动接口。

`wireguard_control_method` 可选：

- `ifup` 或 `openwrt`：`ifdown wg0` / `ifup wg0`
- `wg-quick`：`wg-quick down wg0` / `wg-quick up wg0`
- `systemd`：`systemctl stop wg-quick@wg0` / `systemctl start wg-quick@wg0`

启动 agent：

```sh
./wgnh agent --config /etc/wgnh/agent.json
```

## 5. 配置 client agent

`office-b` 上的 `/etc/wgnh/agent.json` 示例：

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

第一次测试可以先设置 `"dry_run": true`，确认流程正常后再改成 `false`。

监控流程：

1. client agent 执行 `wg show wg0 latest-handshakes`。
2. peer 连续超过阈值没有 handshake 后，上报给 daemon。
3. daemon 找到匹配的 binding。
4. daemon 给 server 节点下发 `natter.run`。
5. server 上报新 endpoint。
6. daemon 给 client 节点下发 `endpoint.apply`。

启动 agent：

```sh
./wgnh agent --config /etc/wgnh/agent.json
```

## 6. 使用 Web UI

本机运行：

```sh
./wgnh web --addr 127.0.0.1:9090
```

或者用 Docker 运行：

```sh
docker run --rm -p 9090:9090 wgnh:local web --addr 0.0.0.0:9090
```

打开 `http://127.0.0.1:9090`，输入：

- daemon TCP 地址：`your-vps.example.com:3333`
- admin token：启动 `wgnh daemon serve --admin-token` 时设置的值

浏览器会把这两个值保存到 localStorage。任何能访问 Web UI 的客户端都可以打开页面，但必须输入正确的 admin token 才能连接 daemon。

Web UI 支持：

- 查看节点在线状态
- 查看 binding 和当前 endpoint
- 查看最近事件和 payload
- 手动触发某个 server/interface 执行 Natter
- 连接成功后每 15 秒自动刷新

## 7. 从 CLI 手动触发 Natter

```sh
./wgnh daemon run-natter \
  --addr your-vps.example.com:3333 \
  --admin-token "$ADMIN_TOKEN" \
  --server-node home-a \
  --server-interface wg0
```

## 8. 从 CLI 查看状态

```sh
./wgnh daemon nodes --addr your-vps.example.com:3333 --admin-token "$ADMIN_TOKEN"
./wgnh daemon bindings --addr your-vps.example.com:3333 --admin-token "$ADMIN_TOKEN"
./wgnh daemon events --addr your-vps.example.com:3333 --admin-token "$ADMIN_TOKEN"
```

## 常见问题

### `invalid node credentials`

检查 `node_id`、token 和 daemon 使用的 `--state` 文件。对同一个节点再次执行 `daemon create-node` 会轮换 token，需要同步更新 agent 配置。

### `connection reset by peer`

TCP 连接被 daemon 或网络中间设备断开。常见原因是 daemon 重启、防火墙策略、NAT 超时或长轮询被中断。agent 会自动重试。

### Web UI 连不上 daemon

Web UI 所在主机或容器必须能连接 daemon TCP 地址。如果 daemon 在 Docker 宿主机上，可以使用宿主机可达 IP，或在 Linux 上用 `--network host`。

## 当前限制

- 自定义 TCP 协议当前是明文。
- 状态存储当前是 JSON 文件。
- 自动监控当前基于客户端 handshake 时间和简单 cooldown。
