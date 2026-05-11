package app

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

type StateProviderStatus struct {
	Exists       bool
	Rows         int
	Distribution map[string]int
}

func (a *App) StateExists() (bool, error) {
	_, err := os.Stat(a.StatePath())
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, fmt.Errorf("stat ~/.codex/state_5.sqlite: %w", err)
}

func (a *App) BackupState(destRoot string) (*BackupFile, error) {
	exists, err := a.StateExists()
	if err != nil || !exists {
		return nil, err
	}
	dst := filepath.Join(destRoot, "files", "state_5.sqlite")
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return nil, fmt.Errorf("create state backup directory: %w", err)
	}
	if err := sqliteBackup(a.StatePath(), dst); err != nil {
		return nil, fmt.Errorf("backup ~/.codex/state_5.sqlite: %w", err)
	}
	info, err := os.Stat(dst)
	if err != nil {
		return nil, fmt.Errorf("stat state backup: %w", err)
	}
	return &BackupFile{RelativePath: "state_5.sqlite", Size: info.Size()}, nil
}

func (a *App) RestoreState(root string, stateFile *BackupFile) error {
	if stateFile == nil {
		return nil
	}
	if stateFile.RelativePath != "state_5.sqlite" {
		return fmt.Errorf("invalid state backup path %q", stateFile.RelativePath)
	}
	src := filepath.Join(root, "files", stateFile.RelativePath)
	info, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("state backup file missing: %w", err)
	}
	if info.IsDir() {
		return errors.New("state backup file is a directory")
	}
	if err := copyFile(src, a.StatePath(), info.Mode().Perm()); err != nil {
		return fmt.Errorf("restore ~/.codex/state_5.sqlite: %w", err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		path := a.StatePath() + suffix
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove stale SQLite sidecar %s: %w", path, err)
		}
	}
	return nil
}

func (a *App) UpdateStateProvider(provider string) (int, error) {
	exists, err := a.StateExists()
	if err != nil || !exists {
		return 0, err
	}
	out, err := runSQLite(a.StatePath(), fmt.Sprintf(
		"UPDATE main.threads SET model_provider = %s WHERE model_provider <> %s; SELECT changes();",
		sqlString(provider),
		sqlString(provider),
	))
	if err != nil {
		return 0, fmt.Errorf("update main.threads.model_provider: %w", err)
	}
	lines := nonEmptyLines(out)
	if len(lines) == 0 {
		return 0, errors.New("sqlite did not report updated row count")
	}
	changed, err := strconv.Atoi(lines[len(lines)-1])
	if err != nil {
		return 0, fmt.Errorf("parse SQLite updated row count %q: %w", lines[len(lines)-1], err)
	}
	return changed, nil
}

func (a *App) StateProviderStatus() (StateProviderStatus, error) {
	exists, err := a.StateExists()
	if err != nil || !exists {
		return StateProviderStatus{Exists: exists}, err
	}
	out, err := runSQLite(a.StatePath(), "SELECT model_provider, COUNT(*) FROM main.threads GROUP BY model_provider ORDER BY model_provider;")
	if err != nil {
		return StateProviderStatus{}, fmt.Errorf("read main.threads.model_provider distribution: %w", err)
	}
	status := StateProviderStatus{Exists: true, Distribution: map[string]int{}}
	for _, line := range nonEmptyLines(out) {
		provider, countText, ok := strings.Cut(line, "|")
		if !ok {
			return StateProviderStatus{}, fmt.Errorf("parse SQLite provider distribution row %q", line)
		}
		count, err := strconv.Atoi(countText)
		if err != nil {
			return StateProviderStatus{}, fmt.Errorf("parse SQLite provider count %q: %w", countText, err)
		}
		status.Distribution[provider] = count
		status.Rows += count
	}
	return status, nil
}

func sqliteBackup(src, dst string) error {
	_, err := runSQLite(src, ".backup main "+sqliteShellString(dst))
	return err
}

func runSQLite(dbPath, command string) (string, error) {
	cmd := exec.Command("sqlite3", "-batch", dbPath, command)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func sqlString(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func sqliteShellString(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func nonEmptyLines(out string) []string {
	raw := strings.Split(out, "\n")
	lines := make([]string, 0, len(raw))
	for _, line := range raw {
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}
