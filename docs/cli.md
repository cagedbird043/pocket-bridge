# CLI Contract

## 目标

CLI 必须短、稳定、可脚本化。

## 命令草案

```bash
pb keygen
pb status
pb notify phone "标题" "内容"
pb clip push phone
pb clip push phone "显式文本"
pb clip pull phone
pb task started "标题" "摘要"
pb task blocked "标题" "摘要"
pb task done "标题" "摘要"
pb task failed "标题" "摘要"
```

当前已实现：

- `pb keygen`
- `pb status`
- `pb notify <target> <title> <body>`
- `pb clip push <target> [text]`
- `pb clip pull <target>`
- `pb task <started|blocked|done|failed> <title> <summary>`

尚未实现：

- `file send *`

## 行为约束

- `pb keygen` 输出一对 `public_key_base64/private_key_base64`，分别给 relay 和设备使用
- `notify` 只发结构化通知，不镜像系统通知，也不支持 pull
- `clip push/pull` 是显式动作，不做后台自动双向覆盖
- `clip push <target>` 在 laptop 上默认读取本机系统剪贴板；若额外给 text，则直接发送该文本
- `clip pull <target>` 会等待远端返回文本，并把结果输出到 stdout
- `file send` 第一阶段只支持单文件
- `task *` 是给 Codex/脚本集成预留的稳定入口
- 笔记本上的 shell 只能作为本地胶水，不参与安卓实现

## 输出风格

CLI 标准输出应尽量机器可读。建议至少支持：

- 默认简洁文本
- `--json`

## 错误语义

统一错误码建议：

- `1`: 本地参数错误
- `2`: agent 不可达
- `3`: relay 不可达
- `4`: 目标设备离线
- `5`: 目标动作被 ACL 拒绝
- `6`: 文件过大或缓存失败
