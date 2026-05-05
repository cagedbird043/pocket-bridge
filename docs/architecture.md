# Architecture

## 概览

`pocket-bridge` 使用固定公网中继，而不是设备间直连。

```text
Android client <-> relay <-> laptop agent <-> local CLI/session bridge
```

## 当前实现状态

- `relay`、`agentd`、`pb` 已完成最小可用链路
- Android 端当前是原生 Kotlin App，形态为 `MainActivity + 前台 BridgeService`
- Android 在线时通过 WebSocket 直接收 `notify.push` 和 `task status`
- Android 离线时可选由 laptop agent 直连 FCM 投递 `notify/task`
- 显式剪贴板 `push/pull` 已落地到 CLI、agent 和 Android demo
- 2026-04-06 已在 Android 15 AVD 上验证双向通知闭环
- 2026-04-06 已在 Wayland laptop + Android 15 AVD 上验证双向剪贴板闭环
- 当前认证已切到 `Ed25519 challenge-response`
- 当前 debug App 允许明文 `ws://223.109.140.254`，用于贴近日常真实路径的 Android 开发 / 验证

## 组件边界

### relay

职责：

- 设备认证
- WebSocket 会话保持
- push 消息路由
- 短时文件缓存
- 设备在线状态维护

不负责：

- 任意命令执行
- 长期对象存储
- GUI 逻辑

### laptop agent

职责：

- 主动连接 relay
- 暴露本地 Unix socket 给 CLI
- 执行白名单动作
- 桥接通知和剪贴板
- 在目标离线时直连 FCM 补发 `notify/task`
- 为 Codex/脚本提供统一事件入口

### local CLI

职责：

- 让本机用户用最短命令驱动 agent
- 保持可脚本化
- 不直接处理公网认证与会话逻辑

### android client

职责：

- 登录和设备持钥
- 以前台 WebSocket 接收实时 push 消息
- 通过 FCM 接收由 laptop 直发的离线 `notify/task`
- 按需读取或写入系统剪贴板
- 展示通知
- 小文件上传下载

强约束：

- 必须是原生 Kotlin App
- 不依赖 shell
- 不依赖 Termux
- 不把通知设计成 pull 模式
- 剪贴板只做显式读写，不做后台自动双向监听

## 设计约束

- 协议必须稳定、窄、可版本化
- 不依赖局域网发现
- 不假设手机能被反向连接
- 不把“后台稳定剪贴板监听”作为第一阶段目标
- 通知默认追求尽快送达，在线时必须优先走实时 push
- 离线通知优先由 laptop 直连系统级 push，而不是假设 Android 后台 WebSocket 长期可靠

## 为什么不用 KDE Connect 路线

- 目标不是镜像系统通知
- 目标不是 LAN first
- 目标不是多媒体控制或设备生态整合
- 目标是服务手机遥控笔记本和 Codex 推送的个人工作流

## 目标安全模型

- 每台设备一把独立设备密钥
- relay 只保存公钥和设备 ACL
- 只允许白名单消息类型
- 高风险动作不能是“任意 shell”
- 文件缓存必须有 TTL

## 当前与目标的差距

- 小文件投递仍未实现
- 明文 `ws` 只应存在于本地开发；公网环境必须收敛到 TLS
- 仓库里的示例密钥只用于 demo，真实部署仍需替换为独立设备密钥
