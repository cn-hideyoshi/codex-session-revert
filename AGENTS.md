# codex-session-revert

## 项目目的

本项目用于批量修改 Codex 会话文件中的 `model_provider` 字段。

目标文件范围：

```text
~/.codex/sessions/*/*/*/*.jsonl
```

程序会读取当前 `~/.codex/config.toml` 中配置的 `model_provider`，并将上述
`.jsonl` 会话文件里的 model_provider 更新为该值。若配置文件中没有显式配置
model_provider，则默认使用：

```text
openai
```

## 安全要求

- 修改任何 `.jsonl` 会话文件前，必须先自动创建备份。
- 备份完成后，命令行必须输出本次备份名，方便用户后续恢复。
- 恢复备份时不得猜测备份名；应使用用户指定的备份，或在交互/文档中明确可用备份。
- 默认只处理 `~/.codex/sessions/*/*/*/*.jsonl` 范围内的文件。
- 解析和写入 `.jsonl` 时应保持逐行 JSONL 语义：一行一个 JSON 对象，不能把文件整体改写成 JSON 数组。

## 命令行

### `backup`

保存当前会话文件备份。

行为要求：

- 扫描 `~/.codex/sessions/*/*/*/*.jsonl`。
- 保存可恢复的备份。
- 完成后输出备份名。
- 不修改 model_provider。

### `revert`

切换会话文件中的 model_provider。

行为要求：

- 执行前自动备份，并输出备份名。
- 默认读取 `~/.codex/config.toml` 中的 model_provider。
- 若配置中没有 model_provider，则使用默认值 `openai`。
- 支持手动传参覆盖目标 model_provider：

```bash
codex-session-revert revert --provider openai
```

### `restore`

恢复备份。

行为要求：

- 将指定备份恢复到 `~/.codex/sessions/*/*/*/*.jsonl`。
- 恢复前应校验备份存在且结构有效。
- 恢复完成后输出恢复的备份名和影响的文件数量。
- 命令行可接受唯一备份名前缀，避免用户输入过长备份名。

### `status`

查询 JSONL 和 model_provider 状态。

行为要求：

- 统计匹配到的 `.jsonl` 文件数量。
- 检查 JSONL 是否可逐行解析。
- 汇总当前发现的 model_provider 分布。
- 显示 `~/.codex/config.toml` 中的目标 model_provider；若未配置，则显示默认值 `openai`。

### `clean`

清理备份。

行为要求：

- 清理项目创建的备份文件。
- 不删除当前会话文件。
- 清理前应能让用户确认目标范围，或仅清理符合项目命名规则的备份。

## 实现约定

- 所有命令应优先给出清晰、可复制的终端输出。
- 错误信息要说明失败原因和下一步建议。
- 对用户数据的修改必须保守；无法解析的 JSONL 行应报告并跳过或中止，不能静默破坏。
- 路径中的 `~` 应按当前用户 home 目录展开。
- model_provider 字段只修改明确存在于会话 JSON 对象中的 model_provider 配置，不应无关改写其他字段。

## 提交约定

- Git 提交信息必须使用 Conventional Commits 风格：

```text
type(scope): subject
```

- 示例：

```text
feat(cli): add restore command
refactor(app): split app package files
docs(readme): mark active language
```

- `type` 优先使用 `feat`、`fix`、`refactor`、`docs`、`test`、`ci`、`chore`。
- `scope` 使用小写英文，描述影响范围，例如 `cli`、`app`、`readme`、`workflow`。
- `subject` 使用简短英文描述，不以句号结尾。
