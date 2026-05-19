

natter 自动 获得 nat 后的 ipv4 地址，并更新 wg endpoint

原本的方案是通过 ssh 远程执行命令实现的（参考 old 目录），但是这样需要配置很多机器的免密登录，太复杂。

想实现一个配置更友好的方式

- 利用一个公网 v4 服务器 vps 提供打通所有机器的基础
- A 作为 wg server（有 nat ipv4 机器）
    - wg0, wg1 等多个 wg 接口
- B, C, D 是需要连接 A 的（指定哪个接口，和 pub key）
- A, B, C, D 通过客户端连接到 vps，vps 上运行一个 daemon 服务
  - 需要一定的加密，不能裸奔。
- 自动监控 B, C, D 是否连接不上了，让 A 通过 natter 重新获得 ip 地址
    - 更新 B, C, D 的 wg endppint（支持 openwrt uci 和 linux wg.conf格式）
- 有一个 web 提供可视化
    - 每个客户端配置，和当前连接状态
- vps 服务需要一定加密