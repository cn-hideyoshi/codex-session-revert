# codex-session-revert

**English** | [简体中文](README.zh-CN.md)

`codex-session-revert` is a small CLI for updating Codex session JSONL files so their `model_provider` matches the provider configured in `~/.codex/config.toml`.

It targets only:

```text
~/.codex/sessions/*/*/*/*.jsonl
```

Before changing any session file, `revert` always creates a restorable backup.

## Install

Build from source:

```bash
go build -o codex-session-revert ./cmd/codex-session-revert
```

Run tests:

```bash
go test ./...
```

## Commands

Create a backup without changing sessions:

```bash
codex-session-revert backup
```

Update session `model_provider` values using `~/.codex/config.toml`:

```bash
codex-session-revert revert
```

Override the target value from the command line:

```bash
codex-session-revert revert --provider openai
```

Control concurrent file workers:

```bash
codex-session-revert revert --provider openai --workers 4
```

Show JSONL validity, file counts, and discovered `model_provider` values:

```bash
codex-session-revert status
```

`status` also accepts `--workers N`.

Restore a backup:

```bash
codex-session-revert restore 20260510
```

`restore` accepts a unique backup-name prefix, so you do not need to type the full backup name.

List backups that would be cleaned:

```bash
codex-session-revert clean --dry-run
```

Delete project-created backups:

```bash
codex-session-revert clean
```

## Backup Format

New backups use short names like:

```text
csr-20260510-010203
```

Backups are stored under:

```text
~/.codex/session-provider-backups/
```

Each backup includes a `manifest.json` and a copy of the matched session files using paths relative to `~/.codex/sessions`.

## Safety

- `revert` backs up before writing.
- JSONL is parsed line by line and kept as JSONL, not rewritten as a JSON array.
- If any matched file has invalid JSONL, no session file is modified.
- Only `model_provider` fields at the top level or under `payload.model_provider` are updated.
- Text content containing the words `model_provider` is not modified.
- File scanning and parsing run with concurrent workers, but writes happen only after validation succeeds.

## Project Layout

```text
cmd/codex-session-revert/  CLI entry point
internal/app/              command implementation and tests
.github/workflows/         CI and release workflows
```

中文说明见 [README.zh-CN.md](README.zh-CN.md).
