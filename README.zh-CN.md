# codex-session-revert

[English](README.md) | **简体中文**

`codex-session-revert` 是一个用于批量修正 Codex 会话 JSONL 文件中 `model_provider` 的命令行工具。

默认处理范围只包含：

```text
~/.codex/sessions/*/*/*/*.jsonl
~/.codex/state_5.sqlite
```

执行修改前，工具会先创建可恢复备份。

## 安装

从源码构建：

```bash
go build -o codex-session-revert ./cmd/codex-session-revert
```

运行测试：

```bash
go test ./...
```

运行时要求：当 `~/.codex/state_5.sqlite` 存在时，系统 `PATH` 中需要可用的 `sqlite3` 命令。

## 命令

只创建备份，不修改会话：

```bash
codex-session-revert backup
```

按 `~/.codex/config.toml` 中的 `model_provider` 更新会话：

```bash
codex-session-revert revert
```

通过命令行覆盖目标值：

```bash
codex-session-revert revert --provider openai
```

指定并发 worker 数：

```bash
codex-session-revert revert --provider openai --workers 4
```

查看 JSONL 状态、文件数量、SQLite thread 状态和当前 `model_provider` 分布：

```bash
codex-session-revert status
```

`status` 同样支持 `--workers N`。

恢复备份：

```bash
codex-session-revert restore 20260510
```

`restore` 支持唯一备份名前缀，不需要输入完整备份名。

预览将被清理的备份：

```bash
codex-session-revert clean --dry-run
```

删除本项目创建的备份：

```bash
codex-session-revert clean
```

## 备份格式

新备份名类似：

```text
csr-20260510-010203
```

备份保存位置：

```text
~/.codex/session-provider-backups/
```

每个备份包含 `manifest.json`，并按相对 `~/.codex/sessions` 的目录结构保存匹配到的 JSONL 文件。
如果 `~/.codex/state_5.sqlite` 存在，备份也会通过 SQLite backup 命令保存一致性副本。

## 安全策略

- `revert` 写入前必定先备份。
- JSONL 按行解析和写入，不会改写成 JSON 数组。
- 只要任意匹配文件存在无法解析的 JSONL 行，本次不会修改任何会话文件。
- 只修改顶层 `model_provider` 或 `payload.model_provider`。
- SQLite 只修改 `~/.codex/state_5.sqlite` 中的 `main.threads.model_provider`。
- 不修改普通文本中出现的 `model_provider` 字样。
- 文件扫描和解析使用并发 worker；所有文件校验通过后才开始写入。

## 目录结构

```text
cmd/codex-session-revert/  CLI 入口
internal/app/              命令实现和测试
.github/workflows/         CI 与发布流程
```

English documentation: [README.md](README.md).
