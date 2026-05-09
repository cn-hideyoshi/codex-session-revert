package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func (a *App) Clean(dryRun bool) (int, []string, error) {
	entries, err := os.ReadDir(a.BackupRoot())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil, nil
		}
		return 0, nil, fmt.Errorf("read backup directory: %w", err)
	}
	names := make([]string, 0)
	for _, entry := range entries {
		if entry.IsDir() && isProjectBackupName(entry.Name()) {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	if dryRun {
		return len(names), names, nil
	}
	for _, name := range names {
		if err := os.RemoveAll(filepath.Join(a.BackupRoot(), name)); err != nil {
			return 0, names, fmt.Errorf("delete backup %s: %w", name, err)
		}
	}
	return len(names), names, nil
}

func isProjectBackupName(name string) bool {
	return strings.HasPrefix(name, backupPrefix) || strings.HasPrefix(name, legacyBackupPrefix)
}
