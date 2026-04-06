# CLI Contract

## 目标

CLI 必须短、稳定、可脚本化。

## 命令草案

```bash
pb status
pb notify phone "标题" "内容"
pb notify laptop "标题" "内容"
pb clip push phone
pb clip pull phone
pb clip push laptop
pb clip pull laptop
pb file send phone ./path/to/file
pb file send laptop ./path/to/file
pb task started "标题" "摘要"
pb task blocked "标题" "摘要"
pb task done "标题" "摘要"
pb task failed "标题" "摘要"
```

## 行为约束

- `notify` 只发结构化通知，不镜像系统通知，也不支持 pull
- `clip push/pull` 是显式动作，不做后台自动双向覆盖
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
