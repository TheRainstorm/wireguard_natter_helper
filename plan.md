# WireGuard Natter Helper 实现方案

## 1. 目标

实现一个比旧 SSH 脚本更易配置、更易运维的 WireGuard endpoint 自动更新系统。

核心目标：

- A 是位于 NAT 后的 WireGuard server，可能有 `wg0`、`wg1` 等多个 WireGuard 接口。
- B、C、D 等客户端需要连接 A 的指定 WireGuard 接口和指定 peer。
- 所有节点通过客户端 agent 主动连接到公网 IPv4 VPS。
- VPS 上运行 daemon，提供控制面、状态协调、加密通信和 Web 可视化。
- 当 B、C、D 连接 A 失败或状态异常时，系统触发 A 使用 natter 重新获取 UDP NAT 映射。
- A 获取新的公网 IPv4 endpoint 后，由 VPS 通知相关客户端更新 WireGuard endpoint。
- 客户端支持更新 OpenWrt UCI 配置和标准 Linux `wg.conf` 配置。

## 2. 旧方案问题

旧方案参考 `old/` 目录：

- A 本地运行 natter。
- natter 成功后执行 `wg-update.sh`。
- `wg-update.sh` 通过 SSH 远程登录客户端机器，修改 `/etc/config/network`。
- 修改后重启客户端 WireGuard 接口。

主要问题：

- 需要为多台机器配置 SSH 免密登录和跳板机，部署成本高。
- A 需要知道所有客户端的 SSH 访问方式，配置复杂且耦合。
- 安全边界不清晰：A 拥有远程登录客户端的能力。
- 缺少统一状态管理和可视化。
- 难以支持不同客户端系统和不同配置格式。

新方案应把 SSH 远程执行改为客户端主动长连接，由各节点只执行本机允许的操作。

## 3. 总体架构

推荐采用中心化控制面架构：

```text
                  HTTPS / WebSocket / mTLS
        +-----------------------------------------+
        |                 VPS                     |
        |-----------------------------------------|
        | daemon                                  |
        | - agent 连接管理                         |
        | - 节点认证与权限                         |
        | - WireGuard peer 拓扑                    |
        | - 状态监控与事件判断                     |
        | - endpoint 发布                          |
        | - Web API                               |
        | - Web UI                                |
        +--------------------+--------------------+
                             ^
                             |
          encrypted agent control channel
                             |
     +-----------+-----------+-----------+-----------+
     |           |           |           |           |
     v           v           v           v           v
   A agent     B agent     C agent     D agent   admin browser
   NAT host    client      client      client
   natter      uci/wgconf  uci/wgconf  uci/wgconf
```

角色划分：

- VPS daemon：可信控制面，不直接登录任何节点。
- A agent：运行在 NAT 后 WireGuard server 上，负责本机 natter、读取 WireGuard 状态、发布 endpoint。
- B/C/D agent：运行在客户端上，负责本机连通性检测、应用 endpoint 更新、重载 WireGuard。
- Web UI：查看配置、节点在线状态、peer 状态、当前 endpoint、历史事件，并提供少量手动操作。

## 4. 技术选型建议

首版建议使用 Go 实现 daemon 和 agent。

理由：

- 单二进制部署，适合 VPS、Linux、OpenWrt。
- 原生支持 HTTP、WebSocket、TLS、systemd service。
- 并发模型适合长连接 agent。
- 容易交叉编译到 `linux/amd64`、`linux/arm64`、`linux/mipsle` 等 OpenWrt 常见平台。

持久化建议：

- 首版使用 SQLite。
- 配置和状态都由 daemon 统一存储。
- agent 本地只保存连接 daemon 所需的身份凭据、本机执行策略和少量缓存。

通信建议：

- 首版使用 HTTPS + WebSocket 长连接。
- 后续可升级到 mTLS 或 Noise Protocol。
- 不建议裸 TCP 自定义协议作为首版，除非明确需要极小运行时依赖。

Web UI 建议：

- 首版可以用 daemon 内嵌静态页面。
- 后端提供 JSON API。
- UI 以表格和详情页为主，不做复杂前端工程也可以满足可视化需求。

## 5. 核心数据模型

### 5.1 Node

表示一个运行 agent 的机器。

字段：

- `id`：稳定节点 ID。
- `name`：显示名称，如 `home-a`、`office-b`。
- `role`：`server` 或 `client`。
- `status`：`online`、`offline`、`degraded`。
- `last_seen_at`：最后心跳时间。
- `agent_version`：agent 版本。
- `platform`：`linux`、`openwrt` 等。
- `allowed_actions`：允许 daemon 下发的动作集合。

### 5.2 WireGuardInterface

表示 A 或客户端上的一个 WireGuard 接口。

字段：

- `id`
- `node_id`
- `name`：如 `wg0`。
- `listen_port`
- `config_type`：`openwrt_uci`、`wg_conf`、`wg_runtime_only`。
- `config_path`：如 `/etc/config/network` 或 `/etc/wireguard/wg0.conf`。
- `reload_command`：受白名单约束的重载方式。

### 5.3 PeerBinding

表示一个客户端 peer 应连接到 A 的哪个接口。

字段：

- `id`
- `server_node_id`：A。
- `server_interface`：如 `wg0`。
- `client_node_id`：B/C/D。
- `client_interface`：客户端本机接口。
- `client_peer_public_key`：客户端配置里代表 A 的 peer public key，或用于匹配 peer 的 key。
- `server_peer_public_key`：A 上对应客户端 peer 的 public key。
- `endpoint_host`
- `endpoint_port`
- `enabled`

### 5.4 EndpointLease

表示 A 通过 natter 获得的一次公网映射。

字段：

- `id`
- `server_node_id`
- `server_interface`
- `protocol`：必须是 `udp`。
- `local_ip`
- `local_port`
- `public_ip`
- `public_port`
- `created_at`
- `expires_at`：可选，natter 未给出时通过策略估算。
- `source_event_id`

### 5.5 Event

记录状态变化和操作历史。

字段：

- `id`
- `type`：如 `node_online`、`peer_unreachable`、`natter_started`、`endpoint_updated`、`reload_failed`。
- `severity`：`info`、`warning`、`error`。
- `node_id`
- `peer_binding_id`
- `message`
- `payload_json`
- `created_at`

## 6. 通信协议

### 6.1 Agent 连接流程

1. agent 读取本地配置。
2. agent 使用节点 token 或客户端证书连接 VPS daemon。
3. daemon 验证身份。
4. daemon 返回该节点的任务配置和心跳参数。
5. agent 建立 WebSocket 长连接。
6. 双方周期性心跳。

### 6.2 Agent 上报消息

建议统一消息格式：

```json
{
  "id": "msg_01",
  "type": "status.report",
  "sent_at": "2026-05-19T12:00:00Z",
  "payload": {}
}
```

常见上报类型：

- `status.report`：节点状态、接口状态、WireGuard 状态摘要。
- `peer.probe_result`：客户端到 A 的连通性检测结果。
- `natter.result`：A 运行 natter 后获得的公网 endpoint。
- `action.result`：执行 daemon 下发动作后的结果。
- `log.tail`：可选，按需上传近期诊断日志。

### 6.3 Daemon 下发消息

常见下发类型：

- `config.sync`：同步节点配置。
- `probe.run`：要求客户端立即检测某个 peer。
- `natter.run`：要求 A 运行 natter。
- `endpoint.apply`：要求客户端更新 endpoint。
- `wg.reload`：要求 agent 重载本机 WireGuard 接口。
- `diagnostic.collect`：收集诊断信息。

所有命令必须带：

- `command_id`
- `deadline`
- `idempotency_key`
- `expected_node_id`
- `action`
- `payload`

agent 必须校验命令是否在本机白名单内，不能执行任意 shell。

## 7. 加密与认证方案

### 7.1 首版安全基线

首版必须满足：

- daemon 只监听 HTTPS。
- agent 到 daemon 使用 TLS。
- 每个节点独立 token。
- token 只在注册或配置文件中出现，不在 Web UI 明文展示。
- daemon 存储 token hash，不存明文 token。
- 每个 agent 只能接收自己节点 ID 的命令。
- agent 本地只允许执行内置动作，不接受 daemon 下发任意 shell。

### 7.2 推荐升级：mTLS

第二阶段加入 mTLS：

- daemon 自建 CA 或导入用户 CA。
- 每个节点使用独立 client certificate。
- 节点吊销通过证书吊销列表或 daemon denylist 实现。

### 7.3 Web 安全

Web UI 至少需要：

- 管理员账号密码。
- 登录 session。
- CSRF 防护。
- 关键操作二次确认，如手动触发 natter、重载接口。
- 审计日志。

## 8. 连通性检测策略

客户端 agent 负责判断自己是否能连上 A。

检测方式按优先级组合：

1. 读取 `wg show <iface> latest-handshakes`。
2. 检查目标 peer 最近 handshake 是否超过阈值。
3. 可选：ping WireGuard 隧道内 A 的地址。
4. 可选：发送应用层 UDP 探测包。

推荐默认策略：

- 每 30 秒读取一次 WireGuard handshake。
- 如果最近 handshake 超过 180 秒，标记为 `suspect`。
- 连续 3 次 `suspect` 后，上报 `peer.unreachable`。
- daemon 聚合多个客户端状态，避免单个客户端网络问题导致频繁 natter。
- 对同一个 A 接口，natter 触发间隔设置冷却时间，如 5 分钟。

触发 natter 的条件建议：

- 某个绑定明确失联，并且 endpoint 当前租约已超过最小有效时间。
- 或同一 A 接口下多个客户端同时失联。
- 或管理员在 Web UI 手动触发。

## 9. Natter 集成

A agent 提供 `natter.run` 动作。

### 9.1 执行方式

首版直接调用外部 natter 命令：

```text
natter.py -i <wan_iface> -u -b <local_port> -e <callback>
```

但不建议继续使用 shell callback 修改远端配置。应改为：

- A agent 启动 natter。
- callback 只把 natter 结果写入本地 agent 可读取的 JSON 文件，或通过 stdout 由 agent 捕获。
- A agent 解析 `protocol/local_ip/local_port/public_ip/public_port`。
- A agent 上报 `natter.result` 到 daemon。
- daemon 写入 `EndpointLease` 并发布给相关客户端。

### 9.2 协议校验

A agent 必须校验：

- `protocol == udp`
- `local_port` 等于对应 WireGuard 接口的 listen port。
- `public_ip` 是有效 IPv4。
- `public_port` 是 1 到 65535。

### 9.3 进程管理

A agent 需要负责：

- 避免同一接口并发运行多个 natter。
- 支持超时取消。
- 记录 stdout/stderr。
- 失败后按退避策略重试。
- 可选：发现旧 natter 进程后优雅停止。

## 10. Endpoint 更新策略

daemon 收到新的 endpoint 后：

1. 找到对应 `server_node_id + server_interface` 下所有启用的 `PeerBinding`。
2. 为每个客户端生成 `endpoint.apply` 命令。
3. 客户端 agent 校验本地接口和 peer public key 是否匹配。
4. 客户端 agent 修改本机配置。
5. 客户端 agent 重载 WireGuard。
6. 客户端 agent 上报结果。
7. daemon 更新绑定状态和事件日志。

## 11. 客户端配置更新实现

### 11.1 OpenWrt UCI

更新路径：

- 使用 `uci` 命令优先于 `sed`。
- 匹配 `config wireguard_<iface>` section 中的 peer public key。
- 更新：
  - `endpoint_host`
  - `endpoint_port`
- 执行 `uci commit network`。
- 重载方式：
  - 首选 `ifdown <iface>; ifup <iface>`。
  - 可配置为 `/etc/init.d/network reload`。

OpenWrt agent 需要支持 dry-run：

- 找到目标 peer section。
- 输出将要变更的字段。
- 不提交修改。

### 11.2 Linux wg.conf

更新路径：

- 解析 `/etc/wireguard/<iface>.conf`。
- 根据 `[Peer]` 的 `PublicKey` 匹配目标 peer。
- 更新 `Endpoint = <ip>:<port>`。
- 写入前创建备份，如 `<iface>.conf.bak.<timestamp>`。
- 尽量保持原文件注释和顺序。
- 重载方式可配置：
  - `wg syncconf <iface> <(wg-quick strip <iface>)`
  - 或 `systemctl restart wg-quick@<iface>`

首版为了降低复杂度，可以实现保守的 ini 分段解析：

- 保留原始行。
- 识别 `[Peer]` block。
- 只替换匹配 block 内的 `Endpoint` 行。
- 如果匹配 block 缺少 `Endpoint`，在该 block 末尾插入。

### 11.3 运行时更新

可选支持只更新运行时：

```text
wg set <iface> peer <public_key> endpoint <ip>:<port>
```

注意：

- 运行时更新不会持久化。
- 可作为快速恢复手段，但仍建议写入配置文件。

## 12. Daemon 模块设计

建议目录结构：

```text
cmd/
  daemon/
  agent/
internal/
  auth/
  config/
  db/
  events/
  protocol/
  server/
  agent/
  wg/
  natter/
  openwrt/
  web/
web/
  static/
```

daemon 主要模块：

- `server`：HTTP server、WebSocket endpoint、Web API。
- `auth`：节点 token、管理员登录、权限校验。
- `db`：SQLite schema 和 repository。
- `protocol`：消息结构、编解码、版本兼容。
- `events`：事件写入和订阅。
- `scheduler`：心跳超时、检测任务、natter 冷却、重试。
- `web`：嵌入静态资源和 API handler。

关键 API：

- `GET /api/nodes`
- `GET /api/nodes/{id}`
- `GET /api/bindings`
- `POST /api/bindings`
- `PATCH /api/bindings/{id}`
- `GET /api/events`
- `POST /api/actions/run-natter`
- `POST /api/actions/apply-endpoint`
- `GET /agent/connect`

## 13. Agent 模块设计

agent 主要模块：

- `client`：连接 daemon、认证、重连、心跳。
- `actions`：执行 daemon 下发的白名单动作。
- `wg`：读取 WireGuard 状态、handshake、peer 信息。
- `probe`：连通性检测。
- `natter`：仅 A 节点启用，负责运行 natter。
- `configwriter`：OpenWrt UCI 和 Linux wg.conf 更新。
- `platform`：区分 OpenWrt/Linux/systemd。

agent 本地配置示例：

```yaml
node_id: home-a
daemon_url: https://vps.example.com
token_file: /etc/wg-natter-helper/token
role: server

wireguard:
  - name: wg0
    listen_port: 51820
    config_type: openwrt_uci
    reload: ifup

natter:
  enabled: true
  wan_interface: pppoe-wan
  command: /usr/bin/natter.py
  timeout_seconds: 60
```

客户端配置示例：

```yaml
node_id: office-b
daemon_url: https://vps.example.com
token_file: /etc/wg-natter-helper/token
role: client

wireguard:
  - name: wg0
    config_type: openwrt_uci
    reload: ifup
    peers:
      - public_key: eVL0uj6T5wEMTs039QF9t+JtXOchHJWROCoq/4kEKlE=
        binding_id: home-a-wg0-office-b
```

## 14. Web UI 设计

首版页面：

- Dashboard：
  - 节点在线数量。
  - 异常 peer 数量。
  - 最近 endpoint。
  - 最近事件。
- Nodes：
  - 节点列表。
  - 在线状态。
  - agent 版本。
  - 最后心跳。
- Bindings：
  - A 接口到客户端 peer 的绑定关系。
  - 当前 endpoint。
  - 最近 handshake。
  - 最近更新结果。
- Events：
  - 时间线。
  - 过滤 node、binding、severity。
- Settings：
  - 注册 token。
  - natter 冷却时间。
  - handshake 阈值。

首版操作：

- 手动触发某个 A 接口运行 natter。
- 手动向某个客户端应用当前 endpoint。
- 禁用或启用某个 binding。
- 查看某个节点的最近诊断结果。

## 15. 配置管理流程

建议首版提供 CLI 初始化，而不是只依赖 Web UI。

### 15.1 daemon 初始化

```text
wgnh daemon init --db /var/lib/wgnh/wgnh.db
wgnh daemon create-admin
wgnh daemon create-node --id home-a --role server
wgnh daemon create-node --id office-b --role client
```

输出节点 token，用户复制到对应机器。

### 15.2 agent 初始化

```text
wgnh agent init \
  --node-id home-a \
  --daemon-url https://vps.example.com \
  --token <token>
```

生成：

- `/etc/wg-natter-helper/agent.yaml`
- `/etc/wg-natter-helper/token`
- systemd service 或 OpenWrt init 脚本。

## 16. 部署方案

### 16.1 VPS daemon

部署方式：

- 单二进制 `/usr/local/bin/wgnh`。
- systemd service。
- SQLite 数据库 `/var/lib/wgnh/wgnh.db`。
- 配置 `/etc/wg-natter-helper/daemon.yaml`。
- TLS 可由 Caddy/Nginx 反代，也可 daemon 内置 TLS。

建议首版使用 Caddy 反代：

- 自动申请证书。
- daemon 只监听 `127.0.0.1:8080`。
- agent 和 Web 都通过 `https://domain` 访问。

### 16.2 Linux agent

部署方式：

- 单二进制。
- systemd service。
- 需要具备读取 WireGuard 状态和修改配置的权限，通常以 root 运行。

### 16.3 OpenWrt agent

部署方式：

- 交叉编译静态二进制。
- init.d 脚本。
- 配置放在 `/etc/wg-natter-helper/`。
- 日志输出到 `logread`。

## 17. 测试方案

### 17.1 单元测试

重点覆盖：

- wg.conf parser 和 writer。
- OpenWrt UCI section 匹配逻辑。
- endpoint 校验。
- daemon 调度条件。
- token hash 和认证。
- protocol message 编解码。

### 17.2 集成测试

使用 Docker 或本地 network namespace 模拟：

- daemon + A agent + B agent。
- B 上报失联。
- daemon 下发 natter。
- A 上报新 endpoint。
- daemon 下发 endpoint.apply。
- B 修改配置并上报成功。

### 17.3 真机测试

至少覆盖：

- 一台 OpenWrt 作为 A。
- 一台 OpenWrt 作为 B。
- 一台普通 Linux 作为 C。
- VPS daemon。

验证内容：

- agent 断线重连。
- VPS 重启后状态恢复。
- natter 失败重试。
- endpoint 更新后 WireGuard handshake 恢复。
- 配置更新失败时不会破坏原配置。

## 18. 迭代里程碑

### Phase 1：最小闭环

目标：替代旧 SSH 方案，实现自动 endpoint 发布。

范围：

- Go 项目骨架。
- daemon HTTPS/WebSocket agent 连接。
- 节点 token 认证。
- SQLite 存储节点、binding、endpoint、event。
- A agent 调用 natter 并上报 endpoint。
- 客户端 agent 接收 endpoint.apply。
- OpenWrt UCI 更新。
- 基础 CLI 配置。

验收：

- A 获取新 endpoint 后，B 能自动更新 `/etc/config/network` 并重启 wg 接口。
- 无需 SSH 免密登录。

### Phase 2：状态监控与自动触发

目标：从手动触发 natter 变为状态驱动。

范围：

- 客户端 handshake 检测。
- daemon 聚合失联事件。
- natter 冷却和退避。
- Linux wg.conf 更新。
- 事件日志。
- 简单 Web Dashboard。

验收：

- B/C/D 失联后，daemon 能自动判断并触发 A natter。
- endpoint 更新后客户端恢复连接。

### Phase 3：可视化和运维完善

目标：让系统适合长期运行。

范围：

- Web 节点管理。
- binding 管理。
- 手动操作按钮。
- 管理员登录。
- 操作审计。
- 诊断信息收集。
- systemd/OpenWrt init 安装命令。

验收：

- 可以通过 Web 查看所有客户端配置和当前连接状态。
- 可以通过 Web 手动触发 natter 和 endpoint 应用。

### Phase 4：安全增强

目标：提高控制面安全等级。

范围：

- mTLS。
- token 轮换。
- 节点吊销。
- 命令签名或消息 HMAC。
- 更细粒度 action 权限。

验收：

- 单个节点 token 泄漏不会影响其他节点。
- 被吊销节点无法继续连接 daemon。

## 19. 关键风险与处理

### 19.1 NAT 映射不稳定

风险：

- natter 获取的 endpoint 可能很快失效。

处理：

- 设置定期保活。
- 根据 handshake 状态提前刷新。
- 对 endpoint lease 设置有效期。

### 19.2 误判客户端失联

风险：

- 客户端自身断网导致 daemon 错误触发 A natter。

处理：

- 使用多次连续失败阈值。
- 聚合同一接口多个客户端状态。
- 设置冷却时间。
- Web UI 标记触发原因。

### 19.3 配置文件写坏

风险：

- 修改 `/etc/config/network` 或 `wg.conf` 失败导致 WireGuard 无法启动。

处理：

- 写入前备份。
- dry-run 校验。
- 只修改匹配 peer 的 endpoint 字段。
- 修改后执行配置校验。
- 失败时恢复备份。

### 19.4 Agent 权限过大

风险：

- agent 通常需要 root 权限。

处理：

- agent 不接受任意 shell 命令。
- 所有动作内置且参数校验。
- 配置文件权限限制为 `0600`。
- daemon 侧按节点配置 allowed actions。

### 19.5 OpenWrt 平台差异

风险：

- 不同 OpenWrt 版本和架构差异较大。

处理：

- 优先通过 `uci` 命令操作。
- 提供架构交叉编译。
- init 脚本保持简单。
- 日志兼容 `logread`。

## 20. 推荐优先级

建议先做最小可用链路：

1. daemon 注册节点和保存 binding。
2. agent 长连接和 token 认证。
3. A agent 手动触发 natter 并上报 endpoint。
4. daemon 向客户端下发 endpoint.apply。
5. OpenWrt 客户端更新 UCI 并重启接口。
6. 加事件日志。
7. 再做 handshake 自动检测和 Web UI。

这样可以最快替代旧 SSH 方案，并且后续每个阶段都能独立验证。
