# pocket-bridge

`pocket-bridge` 是一个为个人工作流设计的窄协议移动桥接系统。

目标不是复刻 KDE Connect，也不是做一个全能远控平台。它只解决这几个明确问题：

- 手机和笔记本之间低延迟推送通知
- 手机和笔记本之间按需收发剪贴板
- 小文件通过云中继做投递
- 笔记本上的 Codex/CLI 工作流可以在任务状态变化时主动通知手机

## 设计原则

- 中继优先，不依赖 P2P 打洞
- 协议收敛，功能白名单化
- 笔记本形态固定为 `CLI + service`
- 安卓客户端必须是原生 App，不依赖 Termux 或 shell
- 通知语义固定为 push，不引入通知 pull
- 先做个人工作流，不做通用产品

## 计划中的组件

- `relay/`
  说明：部署在固定公网服务器上的中继服务，负责认证、消息转发、短时文件缓存和设备会话管理。
- `agent/`
  说明：笔记本常驻 agent，负责与中继保持长连接、执行白名单动作、桥接本地通知和剪贴板。
- `cli/`
  说明：本地命令行入口，向 agent 发命令，不直接接触公网协议。
- `android/`
  说明：安卓原生 Kotlin 客户端，负责通知、按需剪贴板操作、小文件收发。
- `proto/`
  说明：跨端共享的 protobuf 协议定义。

## 第一阶段目标

- 用 protobuf 固定消息格式
- 先打通 `notify.push`
- 再打通 `clipboard push/pull`
- 最后补上小文件投递
- 为 Codex 工作流预留 `task status` 事件类型

## 当前仓库状态

当前仓库已经包含：

- 初版 `roadmap.md`
- 初版架构说明
- 初版 CLI 合同
- 初版 protobuf 草案
- `relay` / `agentd` / `pb` 的 M1 最小实现
- 本地双 agent demo 所需示例配置

当前已经实现的命令：

- `pb status`
- `pb notify <target> <title> <body>`
- `pb task <started|blocked|done|failed> <title> <summary>`

## Quickstart

先生成 protobuf 并构建：

```bash
make proto
go build ./...
```

本地 demo：

```bash
go run ./cmd/relay -config configs/relay.example.json
go run ./cmd/agentd -config configs/agent.laptop.example.json
go run ./cmd/agentd -config configs/agent.phone.example.json
go run ./cmd/pb --socket /tmp/pocket-bridge-laptop.sock status
go run ./cmd/pb --socket /tmp/pocket-bridge-laptop.sock notify phone "M1 ok" "relay agent cli path is alive"
go run ./cmd/pb --socket /tmp/pocket-bridge-laptop.sock task done "Codex task" "task status bridge is alive"
```
