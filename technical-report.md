# WGNH 新项目重构技术报告

## 1. 项目定位

WGNH 的核心目标是让多个 WireGuard 节点在 NAT 环境下更容易互联。系统通过一个部署在 VPS 上的控制端统一管理节点、网络域和 endpoint 更新流程，节点侧只需要运行 agent，并根据控制端下发的配置执行本机 WireGuard 和 NAT 映射相关操作。

新项目应优先追求简单清晰：

- VPS 上运行 daemon，负责控制面、domain 创建、节点准入、配置下发和事件记录。
- 各类机器运行 agent，包括普通 Linux、OpenWrt 路由器等。
- agent 节点可在不同 domain 中扮演不同角色，并使用不同 WireGuard 接口。
- Web UI 作为主要管理入口，尽量减少手写 JSON 配置。

## 2. 核心概念

### 2.1 Daemon

daemon 是部署在 VPS 上的控制平面，负责维护全局配置和调度节点动作。

主要职责：

- 创建和管理 domain。
- 接收 agent 注册和心跳。
- 管理节点审批、命名、删除和配置。
- 维护每个 domain 内的节点角色和 WireGuard 接口绑定关系。
- 根据 server 节点上报的公网 endpoint，向 client 节点下发 WireGuard endpoint 更新命令。
- 提供 Web UI 和管理 API。
- 记录清晰的事件日志，便于排查问题。

daemon 不应该直接承担节点侧 WireGuard 修改逻辑，也不应该把 domain 相关配置写到物理节点本身。

### 2.2 Agent

agent 运行在每台参与组网的机器上。它代表一个物理节点，负责和 daemon 建立连接、上报本机状态，并执行 daemon 下发的命令。

主要职责：

- 首次启动时生成或加载本机身份。
- 连接 daemon，并等待管理员在 Web UI 中批准。
- 上报本机平台、版本、WireGuard 接口发现结果和运行状态。
- 按 domain membership 接收配置。
- 作为 server 时执行 NAT 映射探测，并上报公网 IP 和端口。
- 作为 client 时修改本机 WireGuard peer endpoint。
- 按需重启或刷新本机 WireGuard 接口。

agent 的启动配置应尽量简化。理想情况下，普通节点只需要：

```bash
wgnh agent --daemon-addr your-vps.example.com:3333
```

节点名称、角色、domain、接口名、节点类型等都应尽量在 Web UI 中配置。

### 2.3 Domain

domain 表示一个独立的 WireGuard 网络或一组需要统一管理的 WireGuard 关系。

推荐模型：

- 一个 domain 对应一个明确的 WireGuard 连接关系集合。
- 同一物理节点可以加入多个 domain。
- 同一物理节点在不同 domain 中可以使用不同 WireGuard 接口。
- 同一物理节点在不同 domain 中可以扮演不同角色。

例如：

| Domain | 节点 | 角色 | WireGuard 接口 |
| --- | --- | --- | --- |
| `home-wg0` | `op1` | server | `wg0` |
| `home-wg0` | `mi4a` | client | `wg0` |
| `home-wg1` | `op1` | server | `wg1` |
| `home-wg1` | `mi4a` | client | `wg1` |

这种模型可以避免把 `role`、`domain_id`、`interface` 错误绑定到物理节点上。

### 2.4 Node

node 只表示一个物理 agent 身份。

node 应只保存和“这台机器是谁”有关的信息：

- `node_id`
- `name`
- 认证 token 或 token hash
- 审批状态
- 最近在线状态
- 平台信息
- agent 版本

node 不应该直接保存：

- role
- domain_id
- WireGuard interface
- config_type
- reload_method
- natter 配置

这些都属于 domain membership。

### 2.5 Domain Membership

domain membership 表示“某个 node 在某个 domain 中如何参与”。

它应该保存：

- domain
- node
- role：server 或 client
- node_type：linux 或 openwrt
- WireGuard 接口名
- server 节点的 NAT 映射命令配置
- client 节点的 endpoint 应用策略

这里才是 `role` 和 `interface` 的归属位置。

## 3. 主要功能流程

### 3.1 创建 Domain

管理员在 Web UI 中创建 domain，例如 `home-wg0`。

daemon 生成 domain 记录，并显示该 domain 下的节点、绑定、自动发现结果和最近事件。

### 3.2 节点加入

节点启动 agent 后连接 daemon。

推荐简化流程：

1. agent 启动，只配置 daemon 地址。
2. daemon 发现新 node，状态显示为待审批。
3. 管理员在 Web UI 中设置节点名称。
4. 管理员把该 node 加入某个 domain。
5. 管理员选择角色、节点类型和 WireGuard 接口。
6. daemon 保存 domain membership，并在后续 poll 中下发给 agent。

### 3.3 Server 节点 NAT 映射

当某个节点在 domain 中被设置为 server：

1. daemon 向 server agent 下发 `natter.run`。
2. server agent 执行 NAT 映射探测命令。
3. server agent 上报公网 IP 和端口。
4. daemon 更新该 domain 下相关 binding 的 endpoint。
5. daemon 向 client agent 下发 endpoint 应用命令。

如果 server 本地 WireGuard 端口被占用，agent 可以按配置临时停止对应 WireGuard 接口，完成 NAT 探测后再启动接口。

### 3.4 Client 节点更新 Endpoint

当 daemon 获得 server 的新公网 endpoint 后：

1. daemon 找到对应 domain 中的 client binding。
2. daemon 下发 endpoint 更新命令。
3. client agent 修改本机 WireGuard peer endpoint。
4. client agent 按节点类型刷新接口。
5. client agent 上报执行结果。

日志中应明确写出：

- 哪个 binding 被更新。
- 哪个 client 节点执行了更新。
- peer endpoint 从什么值变成什么值。
- 是否触发了接口刷新。
- 操作是否成功。

## 4. Web UI 设计目标

Web UI 应作为主要配置入口，避免用户直接编辑复杂 JSON。

推荐页面结构：

- Domain 列表。
- 当前 domain 详情页。
- 当前 domain 下的成员节点。
- 当前 domain 下的 server/client 关系。
- WireGuard 自动发现信息。
- endpoint 当前状态。
- 最近事件和执行日志。

节点配置界面应简化：

- 节点类型只选择 `OpenWrt` 或 `Linux`。
- 选择节点类型后自动推导 WireGuard 配置方式和接口刷新方式。
- 不直接暴露 `config_type`、`reload_method`、`natter_wireguard_control` 等底层字段。
- 只在高级设置中暴露少量必要参数，例如自定义 natter 命令。

## 5. 数据边界建议

新项目应明确区分“配置数据”和“运行时数据”。

### 5.1 配置数据

配置数据是用户在 Web UI 中主动创建或修改的内容，应稳定保存。

包括：

- domains
- nodes
- domain memberships
- bindings
- server NAT 映射命令配置
- 管理员配置项

### 5.2 运行时数据

运行时数据会频繁变化，不应污染主要配置文件。

包括：

- 节点心跳时间
- 在线状态
- WireGuard 自动发现结果
- endpoint lease
- 命令队列和执行结果
- 最近事件

建议拆分存储：

- 主配置文件：保存稳定配置。
- runtime 文件或数据库表：保存当前运行状态。
- event log 文件：保存事件日志，并限制最大大小或保留天数。

这样可以避免主配置文件不断变化，也方便备份和审计。

## 6. 日志要求

日志必须面向排障，而不是只记录“发生了某件事”。

推荐每条事件包含：

- 时间。
- actor：谁触发了操作，例如 admin、daemon、agent node。
- target：操作对象，例如 node、domain、binding、interface。
- action：具体动作。
- before/after：关键字段变化。
- result：成功或失败。
- error：失败原因。

示例事件表达：

```text
admin updated membership: domain=home-wg0 node=op1 role=server interface=wg0 node_type=openwrt
server endpoint updated: domain=home-wg0 node=op1 interface=wg0 public=1.2.3.4:45678 bindings=1
client endpoint applied: domain=home-wg0 node=mi4a interface=wg0 peer=abc123 endpoint=1.2.3.4:45678 changed=true reload=ifup
```

日志应能回答这些问题：

- 谁触发了操作？
- 操作影响了哪个 domain？
- 操作影响了哪个 node 和接口？
- endpoint 更新成了什么？
- client 是否真的应用成功？
- WireGuard 是否被重启或刷新？

## 7. 推荐的新架构

推荐采用四层模型：

### 7.1 Control Plane

daemon 和 Web UI 属于控制面。

职责：

- 管理 domain。
- 管理节点准入。
- 管理 domain membership。
- 生成命令。
- 聚合状态和日志。

### 7.2 Agent Runtime

agent 属于节点执行层。

职责：

- 上报本机状态。
- 执行命令。
- 修改 WireGuard。
- 执行 NAT 映射探测。

### 7.3 Domain Model

domain model 是业务核心。

核心关系：

```text
node 1 --- n domain_membership n --- 1 domain
domain 1 --- n binding
binding links server membership to client membership
```

这能天然支持一个节点加入多个 domain，并且不同 domain 使用不同接口。

### 7.4 Storage Model

存储应围绕配置和运行时拆分。

建议：

- 配置：JSON、SQLite 或轻量数据库。
- 运行时状态：独立 runtime 存储。
- 事件日志：JSONL 或数据库事件表，支持大小限制和分页读取。

如果新项目希望长期维护，SQLite 会比单个 JSON 文件更适合，因为它更容易支持分页、查询、事件日志和并发写入。

## 8. 非目标

新项目不应优先支持复杂的底层自定义。

建议暂时不要把以下内容暴露给普通用户：

- 手动选择 `config_type`。
- 手动选择 `reload_method`。
- 手动选择 `natter_wireguard_control`。
- 手动维护复杂 binding JSON。

这些可以作为高级配置保留，但默认体验应尽量像 ZeroTier：

1. 启动 agent。
2. 控制面看到节点。
3. 管理员批准并加入 domain。
4. 选择角色和接口。
5. 系统自动发现并应用 endpoint。

## 9. 重构结论

新项目应围绕“domain membership”重新设计，而不是在 node 上继续挂载 role、domain 和 interface。

最重要的设计原则：

- node 表示机器身份。
- domain 表示一个独立 WireGuard 网络。
- membership 表示机器在某个 domain 中的角色和接口。
- daemon 负责控制和调度。
- agent 负责本机执行。
- Web UI 是主要配置入口。
- 配置和运行时状态必须分离。
- 日志必须能直接支持排障。

按这个模型重构后，项目会更接近 ZeroTier 的使用体验，同时保留 WGNH 的核心能力：在 NAT 环境下自动发现、更新和维护 WireGuard endpoint。
