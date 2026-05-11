package app

import (
	"fmt"
	"path/filepath"
	"sort"
)

func (a *App) SessionRoot() string {
	return filepath.Join(a.Home, ".codex", "sessions")
}

func (a *App) BackupRoot() string {
	return filepath.Join(a.Home, ".codex", "session-provider-backups")
}

func (a *App) ConfigPath() string {
	return filepath.Join(a.Home, ".codex", "config.toml")
}

func (a *App) StatePath() string {
	return filepath.Join(a.Home, ".codex", "state_5.sqlite")
}

func (a *App) SessionFiles() ([]string, error) {
	pattern := filepath.Join(a.SessionRoot(), "*", "*", "*", "*.jsonl")
	paths, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("scan sessions: %w", err)
	}
	sort.Strings(paths)
	return paths, nil
}
