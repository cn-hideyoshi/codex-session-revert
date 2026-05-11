package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type BackupManifest struct {
	Name       string       `json:"name"`
	CreatedAt  string       `json:"created_at"`
	SessionDir string       `json:"session_dir"`
	Files      []BackupFile `json:"files"`
	StateFile  *BackupFile  `json:"state_file,omitempty"`
}

type BackupFile struct {
	RelativePath string `json:"relative_path"`
	Size         int64  `json:"size"`
}

func (a *App) Backup() (string, int, error) {
	files, err := a.SessionFiles()
	if err != nil {
		return "", 0, err
	}
	name, err := a.nextBackupName()
	if err != nil {
		return "", 0, err
	}
	destRoot := filepath.Join(a.BackupRoot(), name)
	filesRoot := filepath.Join(destRoot, "files")
	if err := os.MkdirAll(filesRoot, 0o700); err != nil {
		return "", 0, fmt.Errorf("create backup directory: %w", err)
	}

	manifest := BackupManifest{
		Name:       name,
		CreatedAt:  a.Now().UTC().Format(time.RFC3339Nano),
		SessionDir: a.SessionRoot(),
		Files:      make([]BackupFile, 0, len(files)),
	}
	for _, path := range files {
		info, err := os.Stat(path)
		if err != nil {
			return "", 0, fmt.Errorf("stat session file %s: %w", path, err)
		}
		rel, err := filepath.Rel(a.SessionRoot(), path)
		if err != nil {
			return "", 0, fmt.Errorf("resolve relative path for %s: %w", path, err)
		}
		if err := validateRelativePath(rel); err != nil {
			return "", 0, fmt.Errorf("invalid session path %s: %w", rel, err)
		}
		dst := filepath.Join(filesRoot, rel)
		if err := copyFile(path, dst, info.Mode().Perm()); err != nil {
			return "", 0, fmt.Errorf("copy %s to backup: %w", path, err)
		}
		manifest.Files = append(manifest.Files, BackupFile{RelativePath: filepath.ToSlash(rel), Size: info.Size()})
	}
	stateFile, err := a.BackupState(destRoot)
	if err != nil {
		return "", 0, err
	}
	if stateFile != nil {
		manifest.StateFile = stateFile
	}
	if err := writeJSONFile(filepath.Join(destRoot, "manifest.json"), manifest, 0o600); err != nil {
		return "", 0, fmt.Errorf("write backup manifest: %w", err)
	}
	count := len(files)
	if manifest.StateFile != nil {
		count++
	}
	return name, count, nil
}

func (a *App) nextBackupName() (string, error) {
	base := backupPrefix + a.Now().UTC().Format("20060102-150405")
	for i := 0; i < 100; i++ {
		name := base
		if i > 0 {
			name = fmt.Sprintf("%s-%02d", base, i+1)
		}
		_, err := os.Stat(filepath.Join(a.BackupRoot(), name))
		if errors.Is(err, os.ErrNotExist) {
			return name, nil
		}
		if err != nil {
			return "", fmt.Errorf("check backup name %s: %w", name, err)
		}
	}
	return "", fmt.Errorf("could not allocate a unique backup name for %s", base)
}

func (a *App) ResolveBackupName(input string) (string, error) {
	input = strings.TrimSpace(input)
	if input == "" || strings.ContainsAny(input, `/\`) {
		return "", fmt.Errorf("invalid backup name %q; run clean --dry-run to list available project backups", input)
	}
	entries, err := os.ReadDir(a.BackupRoot())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("backup directory does not exist: %s", a.BackupRoot())
		}
		return "", fmt.Errorf("read backup directory: %w", err)
	}
	var matches []string
	for _, entry := range entries {
		if !entry.IsDir() || !isProjectBackupName(entry.Name()) {
			continue
		}
		name := entry.Name()
		shortName := strings.TrimPrefix(name, backupPrefix)
		legacyShortName := strings.TrimPrefix(name, legacyBackupPrefix)
		if name == input || strings.HasPrefix(name, input) || strings.HasPrefix(shortName, input) || strings.HasPrefix(legacyShortName, input) {
			matches = append(matches, name)
		}
	}
	sort.Strings(matches)
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("backup %q not found; run clean --dry-run to list available project backups", input)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("backup name %q is ambiguous; matches: %s", input, strings.Join(matches, ", "))
	}
}

func (a *App) Restore(name string) (string, int, error) {
	resolvedName, err := a.ResolveBackupName(name)
	if err != nil {
		return "", 0, err
	}
	root := filepath.Join(a.BackupRoot(), resolvedName)
	manifestPath := filepath.Join(root, "manifest.json")
	var manifest BackupManifest
	if err := readJSONFile(manifestPath, &manifest); err != nil {
		return "", 0, fmt.Errorf("read backup manifest: %w", err)
	}
	if manifest.Name != resolvedName {
		return "", 0, fmt.Errorf("backup manifest name mismatch: got %q", manifest.Name)
	}
	if manifest.Files == nil {
		return "", 0, errors.New("backup manifest is missing files list")
	}

	for _, file := range manifest.Files {
		rel := filepath.FromSlash(file.RelativePath)
		if err := validateRelativePath(rel); err != nil {
			return "", 0, fmt.Errorf("invalid backup file path %q: %w", file.RelativePath, err)
		}
		src := filepath.Join(root, "files", rel)
		info, err := os.Stat(src)
		if err != nil {
			return "", 0, fmt.Errorf("backup file missing %q: %w", file.RelativePath, err)
		}
		if info.IsDir() {
			return "", 0, fmt.Errorf("backup file %q is a directory", file.RelativePath)
		}
	}
	if manifest.StateFile != nil {
		if manifest.StateFile.RelativePath != "state_5.sqlite" {
			return "", 0, fmt.Errorf("invalid state backup path %q", manifest.StateFile.RelativePath)
		}
		src := filepath.Join(root, "files", manifest.StateFile.RelativePath)
		info, err := os.Stat(src)
		if err != nil {
			return "", 0, fmt.Errorf("state backup file missing: %w", err)
		}
		if info.IsDir() {
			return "", 0, errors.New("state backup file is a directory")
		}
	}

	count := 0
	for _, file := range manifest.Files {
		rel := filepath.FromSlash(file.RelativePath)
		src := filepath.Join(root, "files", rel)
		dst := filepath.Join(a.SessionRoot(), rel)
		info, err := os.Stat(src)
		if err != nil {
			return resolvedName, count, fmt.Errorf("stat backup file %q: %w", file.RelativePath, err)
		}
		if err := copyFile(src, dst, info.Mode().Perm()); err != nil {
			return resolvedName, count, fmt.Errorf("restore %q: %w", file.RelativePath, err)
		}
		count++
	}
	if manifest.StateFile != nil {
		if err := a.RestoreState(root, manifest.StateFile); err != nil {
			return resolvedName, count, err
		}
		count++
	}
	return resolvedName, count, nil
}
