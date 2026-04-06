# roadmap

## 项目目标

把“手机作为控制面、笔记本作为执行面”的个人工作流收敛成一个稳定的小系统，替代轮询式 Termux/手工 SSH 查看状态的做法。

## 非目标

- 不复刻 KDE Connect
- 不做局域网广播发现
- 不做任意远程 shell
- 不做系统通知镜像
- 不做通知 pull
- 不做大文件同步盘
- 不做通用多用户平台

## 当前状态

- `M0` 已完成：协议、CLI 合同、架构边界已冻结到文档和 proto
- `M1` 已完成：`relay + agentd + pb` 最小链路已实现并提交
- `M2` 已跑通 Android demo：2026-04-06 在 Android 15 AVD 上完成双向通知闭环
- 当前认证仍是静态 token，设备密钥认证还没开始做

## 里程碑

### M0: 合同冻结

- 冻结功能边界
- 冻结消息模型
- 冻结设备角色
- 明确本地 CLI、agent、中继、安卓端的责任分层

完成标准：

- `README.md`
- `roadmap.md`
- `docs/architecture.md`
- `docs/cli.md`
- `proto/bridge.proto`

### M1: Relay + Agent 最小链路

- 笔记本 agent 可与服务器建立认证后的 WebSocket 长连接
- 服务器可维护设备在线状态
- 本地 CLI 可通过 Unix socket 把命令发给 agent
- 可从本地 CLI 向“手机设备”发送一条结构化消息

完成标准：

- `agent status` 可返回在线状态
- `agent notify phone "title" "body"` 可经过 relay 投递
- 日志中可看到 request id / device id / ack

### M2: 通知闭环

- 手机前台能即时接收来自笔记本的通知消息
- 手机能向笔记本发送通知
- 笔记本收到通知后能调用 `notify-send`
- 通知语义保持 push-only，不设计 pull
- 安卓端实现必须是原生 Kotlin，不依赖 shell 或 Termux

当前进展：

- 已在 Android 15 AVD 上验证 `MainActivity + 前台 BridgeService + WebSocket` 路径
- 已验证 `pb notify phone ...` 可在 Android 端展示系统通知
- 已验证 Android 端手动发送通知可投递到 laptop agent
- 当前 Android demo 为了走 AVD `10.0.2.2` 开发链路，允许明文 `ws`

完成标准：

- 笔记本 -> 手机
- 手机 -> 笔记本
- 两端消息均带标题、正文、来源、时间戳
- 在线设备走 WebSocket 立即推送

### M3: 剪贴板闭环

- `clip push`
- `clip pull`
- 安卓端仅在前台读取或写入剪贴板
- Linux 侧通过 `wl-copy` / `wl-paste` 完成桥接

完成标准：

- `agent clip push phone`
- `agent clip pull phone`
- `agent clip push laptop`
- `agent clip pull laptop`

### M4: 小文件投递

- 单文件上传和下载
- 中继提供短期缓存
- 明确大小限制和过期时间

建议约束：

- 默认单文件上限 `32 MiB`
- 默认缓存 TTL `24h`

### M5: Codex 通知集成

- 为任务型会话输出标准状态事件
- 状态包括：
  - `task.started`
  - `task.blocked`
  - `task.done`
  - `task.failed`
- 先通过显式 CLI/脚本集成，再考虑 skill 层自动化

完成标准：

- 笔记本本地可调用一个稳定入口发送任务状态到手机
- 事件体至少包含任务标题、摘要、结果类型、时间戳

## 技术选型

- Relay: Go
- Laptop agent: Go
- CLI: Go
- Android: Kotlin
- Wire protocol: protobuf
- Real-time transport: WebSocket
- Android notification path: 在线时 WebSocket，离线后台视需要加 FCM

## 当前优先级

1. 先把协议和 CLI 合同写稳
2. 先做通知，不先做文件
3. 安卓坚持原生实现，不走 shell/Termux 捷径
4. 先做显式 Codex 通知入口，不先做复杂自动触发
