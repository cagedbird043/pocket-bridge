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
- Android 原生 Kotlin demo 工程
- `MainActivity + BridgeService` 的前台连接形态
- laptop 直连 FCM 的离线 `notify/task` 推送骨架
- AVD 上验证过的双向通知闭环
- Wayland laptop + Android AVD 上验证过的显式剪贴板闭环

当前已经实现的命令：

- `pb keygen`
- `pb status`
- `pb notify <target> <title> <body>`
- `pb clip push <target> [text]`
- `pb clip pull <target>`
- `pb task <started|blocked|done|failed> <title> <summary>`

当前实现边界：

- relay / agent / Android 都已使用同一份 protobuf `Envelope`
- Android 端在线时通过前台 WebSocket 收消息
- Android 端离线通知和任务状态可选由 laptop agent 直连 FCM 补发
- 剪贴板当前是显式 `push/pull`，不做后台自动双向覆盖
- laptop 侧当前通过 `wl-copy` / `wl-paste` 桥接系统剪贴板
- 当前认证已经切到 `Ed25519 challenge-response`
- relay 侧只保存 `public_key_base64`
- agent / Android 侧只保存 `private_key_base64`

## 标准安装

这套仓库现在不需要再靠 `go run` 才能使用。

先构建固定名字的二进制：

```bash
make build
```

安装到系统路径：

```bash
sudo make install install-completion install-systemd
```

安装后会得到：

- `/usr/local/bin/pb`
- `/usr/local/bin/pocket-bridge-relay`
- `/usr/local/bin/pocket-bridge-agentd`
- `/usr/local/share/zsh/site-functions/_pb`
- `/etc/systemd/system/pocket-bridge-relay@.service`
- `/etc/systemd/system/pocket-bridge-agentd@.service`

`pocket-bridge-agentd@.service` 是系统级 unit，但进程实际以指定用户身份运行，例如 `pocket-bridge-agentd@alice.service`。
这样可以保证服务由 PID 1 管理、可开机自启，同时仍然使用该用户的家目录、Wayland 和通知环境。

## 部署约定

推荐的实际配置文件路径：

- relay: `/etc/pocket-bridge/relay-alice.json`
- relay service: `pocket-bridge-relay@alice.service`
- agent: `/etc/pocket-bridge/agentd-alice.json`
- agent env: `/etc/pocket-bridge/agentd-alice.env`
- agent service: `pocket-bridge-agentd@alice.service`

推荐的公网 relay 入口：

- `ws://your-relay-host:18080/ws`

如果要启用 Android 离线推送，agent 配置可参考：

- `configs/agent.laptop.fcm.example.json`

当前默认仍是明文 `ws://`，因为这样最容易在 Android 真机上直接跑通。
它已经有设备级 `Ed25519 challenge-response` 认证，但不提供传输层机密性。
如果后续要承载敏感剪贴板内容，应该进一步切到 `wss://`。

## Firebase / FCM 配置

这一步是可选增强，不影响当前纯 WebSocket live path。

如果你要让 Android 在离线或被厂商后台收敛后仍能收到 `notify/task`，需要额外准备：

- 在 Firebase 控制台创建 Android App，包名必须是 `top.miceworld.pocketbridge`
- 下载 `google-services.json` 放到 `android/app/google-services.json`
- 在 Firebase 控制台为 laptop agent 准备 service account JSON
- 把 service account JSON 放到笔记本，例如 `/etc/pocket-bridge/firebase-service-account.json`
- 在 agent 配置里加上 `fcm.project_id`、`fcm.credentials_file`、`fcm.token_store_path`

仓库当前已做了一个兼容处理：

- 即使没有 `android/app/google-services.json`，Android 仍可正常编译
- 只是此时 FCM 会自动失效，继续退回“前台 WebSocket 可用”的路径

推荐 agent 配置片段：

```json
{
  "device_id": "laptop",
  "private_key_base64": "...",
  "relay_url": "ws://your-relay-host:18080/ws",
  "unix_socket": "/run/user/1000/pocket-bridge.sock",
  "targets": {
    "phone": "phone"
  },
  "fcm": {
    "project_id": "your-firebase-project-id",
    "credentials_file": "/etc/pocket-bridge/firebase-service-account.json",
    "token_store_path": "~/.local/state/pocket-bridge/fcm-tokens.json"
  }
}
```

启用后的工作流：

- 手机前台连上 relay 后，会通过已认证的 WebSocket 把 FCM token 发给 laptop
- laptop agent 把 token 持久化到 `token_store_path`
- 当 laptop 通过 relay 发现手机离线时，会直接调用 FCM 发 `data message`
- 手机上的 `FirebaseMessagingService` 被唤醒后展示系统通知

## Quickstart

先生成 protobuf 并构建：

```bash
make proto
go build ./...
```

如果要生成自己的设备密钥：

```bash
go run ./cmd/pb keygen
```

返回结果里：

- `public_key_base64` 写进 relay 配置
- `private_key_base64` 写进对应设备配置

仓库里的 `configs/*.example.json` 和 Android 默认值已经带了一组仅用于本地 demo 的开发密钥。

本地 demo：

```bash
go run ./cmd/relay -config configs/relay.example.json
go run ./cmd/agentd -config configs/agent.laptop.example.json
go run ./cmd/agentd -config configs/agent.phone.example.json
go run ./cmd/pb --socket /tmp/pocket-bridge-laptop.sock status
go run ./cmd/pb --socket /tmp/pocket-bridge-laptop.sock notify phone "M1 ok" "relay agent cli path is alive"
go run ./cmd/pb --socket /tmp/pocket-bridge-laptop.sock clip push phone
go run ./cmd/pb --socket /tmp/pocket-bridge-laptop.sock clip pull phone
go run ./cmd/pb --socket /tmp/pocket-bridge-laptop.sock task done "Codex task" "task status bridge is alive"
```

Android AVD demo：

```bash
go run ./cmd/relay -config configs/relay.example.json
go run ./cmd/agentd -config configs/agent.laptop.example.json

cd android
./gradlew :app:assembleDebug
adb install -r app/build/outputs/apk/debug/app-debug.apk
adb shell am start -n top.miceworld.pocketbridge/.MainActivity
```

当前 Android demo 的默认值：

- relay URL: `ws://10.0.2.2:18080/ws`
- device id: `phone`
- private key: 使用与 `configs/relay.example.json` 匹配的开发私钥
- notify target: `laptop`

真机调试安装可以直接走：

```bash
cd android
./gradlew :app:assembleDebug
adb install -r app/build/outputs/apk/debug/app-debug.apk
../scripts/provision-android-debug.sh \
  --relay-url ws://your-relay-host:18080/ws \
  --private-key-base64 '<phone private key>' \
  --device-id phone \
  --notify-target laptop \
  --start
```

这个脚本依赖 debug build 的 `run-as`，会直接写入 `shared_prefs`，避免手工在手机上敲 relay URL 和私钥。

已验证的 live path：

- laptop CLI -> relay -> Android App -> Android 系统通知
- Android App -> relay -> laptop agent -> `notify-send`
- laptop clipboard -> relay -> Android clipboard
- Android clipboard -> relay -> laptop clipboard

开发注意：

- 当前 debug App 为了 AVD 直连宿主机 relay，显式允许了明文 `ws://10.0.2.2`
- 这只是本地开发路径；公网部署应切到 `wss://` + TLS
- Android 侧剪贴板目前按用户显式动作工作，符合“前台读写、不要后台自动覆盖”的边界
- FCM 当前只接入 `notify/task` 的离线补发，不承诺剪贴板和文件传输在后台可靠唤醒
- relay 不再承担 FCM 发送；它只负责在线时的实时中转
- 真实部署时请用 `pb keygen` 重新生成每台设备的独立密钥，不要继续使用仓库里的 demo 密钥
