package app

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultProvider    = "openai"
	targetField        = "model_provider"
	backupPrefix       = "csr-"
	legacyBackupPrefix = "codex-session-revert-"
)

type App struct {
	Home    string
	Now     func() time.Time
	Out     io.Writer
	Err     io.Writer
	Workers int
}

type BackupManifest struct {
	Name       string       `json:"name"`
	CreatedAt  string       `json:"created_at"`
	SessionDir string       `json:"session_dir"`
	Files      []BackupFile `json:"files"`
}

type BackupFile struct {
	RelativePath string `json:"relative_path"`
	Size         int64  `json:"size"`
}

type LineProblem struct {
	Path string
	Line int
	Err  error
}

type RewritePlan struct {
	Path    string
	Content []byte
	Changed int
}

func NewApp() (*App, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return &App{
		Home:    home,
		Now:     time.Now,
		Out:     os.Stdout,
		Err:     os.Stderr,
		Workers: runtime.NumCPU(),
	}, nil
}

func (a *App) Run(args []string) error {
	if len(args) == 0 {
		a.printUsage()
		return nil
	}

	switch args[0] {
	case "backup":
		fs := flag.NewFlagSet("backup", flag.ContinueOnError)
		fs.SetOutput(a.Err)
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() != 0 {
			return errors.New("backup does not accept positional arguments")
		}
		name, count, err := a.Backup()
		if err != nil {
			return err
		}
		fmt.Fprintf(a.Out, "Backup: %s\nFiles: %d\n", name, count)
	case "revert":
		fs := flag.NewFlagSet("revert", flag.ContinueOnError)
		fs.SetOutput(a.Err)
		provider := fs.String("provider", "", "override target model_provider")
		workers := fs.Int("workers", a.Workers, "number of concurrent session-file workers")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() != 0 {
			return errors.New("revert does not accept positional arguments")
		}
		if *workers < 1 {
			return errors.New("workers must be greater than 0")
		}
		a.Workers = *workers
		target := strings.TrimSpace(*provider)
		if target == "" {
			var configured bool
			var err error
			target, configured, err = a.ConfigModelProvider()
			if err != nil {
				return err
			}
			if configured {
				fmt.Fprintf(a.Out, "Target model_provider: %s (from ~/.codex/config.toml)\n", target)
			} else {
				fmt.Fprintf(a.Out, "Target model_provider: %s (default)\n", target)
			}
		} else {
			fmt.Fprintf(a.Out, "Target model_provider: %s (from --provider)\n", target)
		}
		name, files, changedLines, err := a.Revert(target)
		if err != nil {
			return err
		}
		fmt.Fprintf(a.Out, "Backup: %s\nFiles scanned: %d\nmodel_provider fields updated: %d\n", name, files, changedLines)
	case "restore":
		fs := flag.NewFlagSet("restore", flag.ContinueOnError)
		fs.SetOutput(a.Err)
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() != 1 {
			return errors.New("restore requires exactly one backup name")
		}
		resolvedName, count, err := a.Restore(fs.Arg(0))
		if err != nil {
			return err
		}
		fmt.Fprintf(a.Out, "Restored backup: %s\nFiles restored: %d\n", resolvedName, count)
	case "status":
		fs := flag.NewFlagSet("status", flag.ContinueOnError)
		fs.SetOutput(a.Err)
		workers := fs.Int("workers", a.Workers, "number of concurrent session-file workers")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() != 0 {
			return errors.New("status does not accept positional arguments")
		}
		if *workers < 1 {
			return errors.New("workers must be greater than 0")
		}
		a.Workers = *workers
		return a.Status()
	case "clean":
		fs := flag.NewFlagSet("clean", flag.ContinueOnError)
		fs.SetOutput(a.Err)
		dryRun := fs.Bool("dry-run", false, "show matching backups without deleting them")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() != 0 {
			return errors.New("clean does not accept positional arguments")
		}
		count, names, err := a.Clean(*dryRun)
		if err != nil {
			return err
		}
		if *dryRun {
			fmt.Fprintf(a.Out, "Matching backups: %d\n", count)
		} else {
			fmt.Fprintf(a.Out, "Deleted backups: %d\n", count)
		}
		for _, name := range names {
			fmt.Fprintf(a.Out, "- %s\n", name)
		}
	case "-h", "--help", "help":
		a.printUsage()
	default:
		a.printUsage()
		return fmt.Errorf("unknown command %q", args[0])
	}
	return nil
}

func (a *App) printUsage() {
	fmt.Fprintln(a.Out, `Usage:
  codex-session-revert backup
  codex-session-revert revert [--provider openai] [--workers N]
  codex-session-revert restore <backup-name>
  codex-session-revert status [--workers N]
  codex-session-revert clean [--dry-run]`)
}

func (a *App) SessionRoot() string {
	return filepath.Join(a.Home, ".codex", "sessions")
}

func (a *App) BackupRoot() string {
	return filepath.Join(a.Home, ".codex", "session-provider-backups")
}

func (a *App) ConfigPath() string {
	return filepath.Join(a.Home, ".codex", "config.toml")
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

func (a *App) workerCount(fileCount int) int {
	workers := a.Workers
	if workers <= 0 {
		workers = runtime.NumCPU()
	}
	if workers < 1 {
		workers = 1
	}
	if fileCount > 0 && workers > fileCount {
		workers = fileCount
	}
	return workers
}

func (a *App) ConfigModelProvider() (string, bool, error) {
	data, err := os.ReadFile(a.ConfigPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return defaultProvider, false, nil
		}
		return "", false, fmt.Errorf("read ~/.codex/config.toml: %w", err)
	}
	provider, ok, err := parseModelProviderFromTOML(data)
	if err != nil {
		return "", false, fmt.Errorf("parse ~/.codex/config.toml model_provider: %w", err)
	}
	if !ok {
		return defaultProvider, false, nil
	}
	return provider, true, nil
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
	if err := writeJSONFile(filepath.Join(destRoot, "manifest.json"), manifest, 0o600); err != nil {
		return "", 0, fmt.Errorf("write backup manifest: %w", err)
	}
	return name, len(files), nil
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

func (a *App) Revert(provider string) (string, int, int, error) {
	if strings.TrimSpace(provider) == "" {
		return "", 0, 0, errors.New("model_provider cannot be empty")
	}
	backupName, _, err := a.Backup()
	if err != nil {
		return "", 0, 0, err
	}

	files, err := a.SessionFiles()
	if err != nil {
		return backupName, 0, 0, err
	}
	plans, problems, err := a.buildRewritePlans(files, provider)
	if err != nil {
		return backupName, 0, 0, err
	}
	totalChanged := 0
	activePlans := make([]RewritePlan, 0, len(plans))
	for _, plan := range plans {
		if plan.Changed > 0 {
			totalChanged += plan.Changed
			activePlans = append(activePlans, plan)
		}
	}
	if len(problems) > 0 {
		return backupName, len(files), 0, formatLineProblems(problems, backupName)
	}

	for _, plan := range activePlans {
		info, err := os.Stat(plan.Path)
		if err != nil {
			return backupName, len(files), totalChanged, fmt.Errorf("stat before write %s: %w", plan.Path, err)
		}
		if err := os.WriteFile(plan.Path, plan.Content, info.Mode().Perm()); err != nil {
			return backupName, len(files), totalChanged, fmt.Errorf("write %s: %w", plan.Path, err)
		}
	}
	return backupName, len(files), totalChanged, nil
}

func (a *App) buildRewritePlans(files []string, provider string) ([]RewritePlan, []LineProblem, error) {
	type job struct {
		Index int
		Path  string
	}
	type result struct {
		Index    int
		Plan     RewritePlan
		Problems []LineProblem
		Err      error
	}

	jobs := make(chan job)
	results := make(chan result, len(files))
	var wg sync.WaitGroup
	for i := 0; i < a.workerCount(len(files)); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				plan, problems, err := buildRewritePlan(j.Path, provider)
				results <- result{Index: j.Index, Plan: plan, Problems: problems, Err: err}
			}
		}()
	}
	go func() {
		for i, path := range files {
			jobs <- job{Index: i, Path: path}
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()

	plans := make([]RewritePlan, len(files))
	problemsByFile := make([][]LineProblem, len(files))
	for r := range results {
		if r.Err != nil {
			return nil, nil, r.Err
		}
		plans[r.Index] = r.Plan
		problemsByFile[r.Index] = r.Problems
	}

	var problems []LineProblem
	for _, fileProblems := range problemsByFile {
		problems = append(problems, fileProblems...)
	}
	return plans, problems, nil
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
	return resolvedName, count, nil
}

func (a *App) Status() error {
	files, err := a.SessionFiles()
	if err != nil {
		return err
	}
	provider, configured, err := a.ConfigModelProvider()
	if err != nil {
		return err
	}

	problems, distribution, lines, err := a.inspectSessionFiles(files)
	if err != nil {
		return err
	}

	source := "default"
	if configured {
		source = "~/.codex/config.toml"
	}
	fmt.Fprintf(a.Out, "Session files: %d\n", len(files))
	fmt.Fprintf(a.Out, "JSONL lines: %d\n", lines)
	if len(problems) == 0 {
		fmt.Fprintln(a.Out, "JSONL parse: ok")
	} else {
		fmt.Fprintf(a.Out, "JSONL parse: %d problem(s)\n", len(problems))
		for _, problem := range problems {
			fmt.Fprintf(a.Out, "- %s:%d: %v\n", problem.Path, problem.Line, problem.Err)
		}
	}
	fmt.Fprintf(a.Out, "Target model_provider: %s (%s)\n", provider, source)
	fmt.Fprintln(a.Out, "Model provider distribution:")
	if len(distribution) == 0 {
		fmt.Fprintln(a.Out, "- <none>: 0")
		return nil
	}
	keys := make([]string, 0, len(distribution))
	for k := range distribution {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(a.Out, "- %s: %d\n", k, distribution[k])
	}
	return nil
}

func (a *App) inspectSessionFiles(files []string) ([]LineProblem, map[string]int, int, error) {
	type job struct {
		Index int
		Path  string
	}
	type result struct {
		Index    int
		Problems []LineProblem
		Counts   map[string]int
		Lines    int
		Err      error
	}

	jobs := make(chan job)
	results := make(chan result, len(files))
	var wg sync.WaitGroup
	for i := 0; i < a.workerCount(len(files)); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				problems, counts, lines, err := inspectJSONL(j.Path)
				results <- result{Index: j.Index, Problems: problems, Counts: counts, Lines: lines, Err: err}
			}
		}()
	}
	go func() {
		for i, path := range files {
			jobs <- job{Index: i, Path: path}
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()

	problemsByFile := make([][]LineProblem, len(files))
	countsByFile := make([]map[string]int, len(files))
	linesByFile := make([]int, len(files))
	for r := range results {
		if r.Err != nil {
			return nil, nil, 0, r.Err
		}
		problemsByFile[r.Index] = r.Problems
		countsByFile[r.Index] = r.Counts
		linesByFile[r.Index] = r.Lines
	}

	distribution := map[string]int{}
	var problems []LineProblem
	lines := 0
	for i := range files {
		lines += linesByFile[i]
		problems = append(problems, problemsByFile[i]...)
		for k, v := range countsByFile[i] {
			distribution[k] += v
		}
	}
	return problems, distribution, lines, nil
}

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

func parseModelProviderFromTOML(data []byte) (string, bool, error) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(stripTomlComment(scanner.Text()))
		if line == "" || strings.HasPrefix(line, "[") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if strings.HasPrefix(key, `"`) || strings.HasPrefix(key, `'`) {
			unquoted, err := strconv.Unquote(key)
			if err == nil {
				key = unquoted
			}
		}
		if key != targetField {
			continue
		}
		value = strings.TrimSpace(value)
		if value == "" {
			return "", false, fmt.Errorf("line %d has empty model_provider value", lineNo)
		}
		if strings.HasPrefix(value, `"`) {
			parsed, err := strconv.Unquote(value)
			if err != nil {
				return "", false, fmt.Errorf("line %d has invalid quoted model_provider: %w", lineNo, err)
			}
			if strings.TrimSpace(parsed) == "" {
				return "", false, fmt.Errorf("line %d has empty model_provider value", lineNo)
			}
			return parsed, true, nil
		}
		if strings.HasPrefix(value, `'`) && strings.HasSuffix(value, `'`) && len(value) >= 2 {
			parsed := value[1 : len(value)-1]
			if strings.TrimSpace(parsed) == "" {
				return "", false, fmt.Errorf("line %d has empty model_provider value", lineNo)
			}
			return parsed, true, nil
		}
		fields := strings.Fields(value)
		if len(fields) == 0 {
			return "", false, fmt.Errorf("line %d has empty model_provider value", lineNo)
		}
		return fields[0], true, nil
	}
	if err := scanner.Err(); err != nil {
		return "", false, err
	}
	return "", false, nil
}

func stripTomlComment(line string) string {
	var out strings.Builder
	inString := false
	var quote rune
	escaped := false
	for _, r := range line {
		if inString {
			out.WriteRune(r)
			if quote == '"' && escaped {
				escaped = false
				continue
			}
			if quote == '"' && r == '\\' {
				escaped = true
				continue
			}
			if r == quote {
				inString = false
			}
			continue
		}
		if r == '#' {
			break
		}
		if r == '"' || r == '\'' {
			inString = true
			quote = r
		}
		out.WriteRune(r)
	}
	return out.String()
}

func buildRewritePlan(path, provider string) (RewritePlan, []LineProblem, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return RewritePlan{}, nil, fmt.Errorf("read %s: %w", path, err)
	}
	lines := bytes.SplitAfter(data, []byte("\n"))
	if len(lines) > 0 && len(lines[len(lines)-1]) == 0 {
		lines = lines[:len(lines)-1]
	}
	var out bytes.Buffer
	var problems []LineProblem
	changed := 0
	for i, rawLine := range lines {
		lineNo := i + 1
		hasNewline := bytes.HasSuffix(rawLine, []byte("\n"))
		line := bytes.TrimSuffix(rawLine, []byte("\n"))
		if bytes.HasSuffix(line, []byte("\r")) {
			line = bytes.TrimSuffix(line, []byte("\r"))
		}
		if len(bytes.TrimSpace(line)) == 0 {
			problems = append(problems, LineProblem{Path: path, Line: lineNo, Err: errors.New("empty line is not a JSON object")})
			out.Write(rawLine)
			continue
		}
		updated, fieldChanges, err := updateProviderInLine(line, provider)
		if err != nil {
			problems = append(problems, LineProblem{Path: path, Line: lineNo, Err: err})
			out.Write(rawLine)
			continue
		}
		changed += fieldChanges
		out.Write(updated)
		if bytes.HasSuffix(rawLine, []byte("\r\n")) {
			out.WriteString("\r\n")
		} else if hasNewline {
			out.WriteByte('\n')
		}
	}
	return RewritePlan{Path: path, Content: out.Bytes(), Changed: changed}, problems, nil
}

func inspectJSONL(path string) ([]LineProblem, map[string]int, int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("read %s: %w", path, err)
	}
	lines := bytes.SplitAfter(data, []byte("\n"))
	if len(lines) > 0 && len(lines[len(lines)-1]) == 0 {
		lines = lines[:len(lines)-1]
	}
	providers := map[string]int{}
	var problems []LineProblem
	for i, rawLine := range lines {
		lineNo := i + 1
		line := bytes.TrimRight(rawLine, "\r\n")
		if len(bytes.TrimSpace(line)) == 0 {
			problems = append(problems, LineProblem{Path: path, Line: lineNo, Err: errors.New("empty line is not a JSON object")})
			continue
		}
		if !json.Valid(line) {
			problems = append(problems, LineProblem{Path: path, Line: lineNo, Err: errors.New("invalid JSON")})
			continue
		}
		providersOnLine, err := stringFieldValues(line, targetField)
		if err != nil {
			problems = append(problems, LineProblem{Path: path, Line: lineNo, Err: err})
			continue
		}
		for _, provider := range providersOnLine {
			providers[provider]++
		}
	}
	return problems, providers, len(lines), nil
}

func updateProviderInLine(line []byte, provider string) ([]byte, int, error) {
	if !json.Valid(line) {
		return nil, 0, errors.New("invalid JSON")
	}
	spans, err := fieldValueSpans(line, targetField)
	if err != nil || len(spans) == 0 {
		return line, 0, err
	}
	quoted, err := json.Marshal(provider)
	if err != nil {
		return nil, 0, err
	}
	changes := 0
	out := append([]byte(nil), line...)
	for i := len(spans) - 1; i >= 0; i-- {
		span := spans[i]
		if bytes.Equal(bytes.TrimSpace(out[span.Start:span.End]), quoted) {
			continue
		}
		next := make([]byte, 0, len(out)-(span.End-span.Start)+len(quoted))
		next = append(next, out[:span.Start]...)
		next = append(next, quoted...)
		next = append(next, out[span.End:]...)
		out = next
		changes++
	}
	if changes == 0 {
		return line, 0, nil
	}
	if !json.Valid(out) {
		return nil, 0, errors.New("internal error: rewritten line is invalid JSON")
	}
	return out, changes, nil
}

type valueSpan struct {
	Start int
	End   int
}

func stringFieldValues(line []byte, field string) ([]string, error) {
	spans, err := fieldValueSpans(line, field)
	if err != nil || len(spans) == 0 {
		return nil, err
	}
	values := make([]string, 0, len(spans))
	for _, span := range spans {
		value := bytes.TrimSpace(line[span.Start:span.End])
		if len(value) == 0 || value[0] != '"' {
			return nil, fmt.Errorf("field %q exists but is not a JSON string", field)
		}
		var parsed string
		if err := json.Unmarshal(value, &parsed); err != nil {
			return nil, fmt.Errorf("parse field %q: %w", field, err)
		}
		values = append(values, parsed)
	}
	return values, nil
}

func fieldValueSpans(line []byte, field string) ([]valueSpan, error) {
	var spans []valueSpan
	end, err := collectFieldValueSpans(line, skipJSONSpace(line, 0), field, nil, &spans)
	if err != nil {
		return nil, err
	}
	if skipJSONSpace(line, end) != len(line) {
		return nil, errors.New("unexpected data after JSON value")
	}
	return spans, nil
}

func collectFieldValueSpans(line []byte, start int, field string, path []string, spans *[]valueSpan) (int, error) {
	if start >= len(line) {
		return 0, errors.New("missing JSON value")
	}
	switch line[start] {
	case '{':
		return collectObjectFieldValueSpans(line, start, field, path, spans)
	case '[':
		return collectArrayFieldValueSpans(line, start, field, path, spans)
	case '"':
		return scanJSONString(line, start)
	default:
		return scanJSONValue(line, start)
	}
}

func collectObjectFieldValueSpans(line []byte, start int, field string, path []string, spans *[]valueSpan) (int, error) {
	i := start + 1
	for {
		i = skipJSONSpace(line, i)
		if i >= len(line) {
			return 0, errors.New("unexpected end of JSON object")
		}
		if line[i] == '}' {
			return i + 1, nil
		}
		if line[i] != '"' {
			return 0, errors.New("expected object key")
		}
		keyStart := i
		keyEnd, err := scanJSONString(line, i)
		if err != nil {
			return 0, err
		}
		var key string
		if err := json.Unmarshal(line[keyStart:keyEnd], &key); err != nil {
			return 0, err
		}
		i = skipJSONSpace(line, keyEnd)
		if i >= len(line) || line[i] != ':' {
			return 0, errors.New("expected ':' after object key")
		}
		valueStart := skipJSONSpace(line, i+1)
		var valueEnd int
		if key == field && isSessionModelProviderPath(path) {
			valueEnd, err = scanJSONValue(line, valueStart)
			if err == nil {
				*spans = append(*spans, valueSpan{Start: valueStart, End: valueEnd})
			}
		} else {
			childPath := append(append([]string(nil), path...), key)
			valueEnd, err = collectFieldValueSpans(line, valueStart, field, childPath, spans)
		}
		if err != nil {
			return 0, err
		}
		i = skipJSONSpace(line, valueEnd)
		if i >= len(line) {
			return 0, errors.New("unexpected end after object value")
		}
		switch line[i] {
		case ',':
			i++
		case '}':
			return i + 1, nil
		default:
			return 0, errors.New("expected ',' or '}' after object value")
		}
	}
}

func collectArrayFieldValueSpans(line []byte, start int, field string, path []string, spans *[]valueSpan) (int, error) {
	i := start + 1
	for {
		i = skipJSONSpace(line, i)
		if i >= len(line) {
			return 0, errors.New("unexpected end of JSON array")
		}
		if line[i] == ']' {
			return i + 1, nil
		}
		valueEnd, err := collectFieldValueSpans(line, i, field, path, spans)
		if err != nil {
			return 0, err
		}
		i = skipJSONSpace(line, valueEnd)
		if i >= len(line) {
			return 0, errors.New("unexpected end after array value")
		}
		switch line[i] {
		case ',':
			i++
		case ']':
			return i + 1, nil
		default:
			return 0, errors.New("expected ',' or ']' after array value")
		}
	}
}

func isSessionModelProviderPath(path []string) bool {
	return len(path) == 0 || (len(path) == 1 && path[0] == "payload")
}

func scanJSONString(line []byte, start int) (int, error) {
	if start >= len(line) || line[start] != '"' {
		return 0, errors.New("expected JSON string")
	}
	escaped := false
	for i := start + 1; i < len(line); i++ {
		if escaped {
			escaped = false
			continue
		}
		switch line[i] {
		case '\\':
			escaped = true
		case '"':
			return i + 1, nil
		}
	}
	return 0, errors.New("unterminated JSON string")
}

func scanJSONValue(line []byte, start int) (int, error) {
	if start >= len(line) {
		return 0, errors.New("missing JSON value")
	}
	switch line[start] {
	case '"':
		return scanJSONString(line, start)
	case '{', '[':
		return scanJSONContainer(line, start)
	default:
		i := start
		for i < len(line) {
			switch line[i] {
			case ',', '}', ']':
				return i, nil
			case ' ', '\t', '\r', '\n':
				return i, nil
			default:
				i++
			}
		}
		return i, nil
	}
}

func scanJSONContainer(line []byte, start int) (int, error) {
	stack := []byte{line[start]}
	inString := false
	escaped := false
	for i := start + 1; i < len(line); i++ {
		if inString {
			if escaped {
				escaped = false
				continue
			}
			switch line[i] {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
			continue
		}
		switch line[i] {
		case '"':
			inString = true
		case '{', '[':
			stack = append(stack, line[i])
		case '}', ']':
			if len(stack) == 0 {
				return 0, errors.New("unexpected container close")
			}
			open := stack[len(stack)-1]
			if (open == '{' && line[i] != '}') || (open == '[' && line[i] != ']') {
				return 0, errors.New("mismatched JSON container")
			}
			stack = stack[:len(stack)-1]
			if len(stack) == 0 {
				return i + 1, nil
			}
		}
	}
	return 0, errors.New("unterminated JSON container")
}

func skipJSONSpace(line []byte, i int) int {
	for i < len(line) {
		switch line[i] {
		case ' ', '\t', '\r', '\n':
			i++
		default:
			return i
		}
	}
	return i
}

func formatLineProblems(problems []LineProblem, backupName string) error {
	var b strings.Builder
	fmt.Fprintf(&b, "JSONL validation failed; no session files were modified. Backup already created: %s. Fix these lines and retry:\n", backupName)
	for _, problem := range problems {
		fmt.Fprintf(&b, "- %s:%d: %v\n", problem.Path, problem.Line, problem.Err)
	}
	return errors.New(strings.TrimRight(b.String(), "\n"))
}

func copyFile(src, dst string, mode fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func writeJSONFile(path string, value any, mode fs.FileMode) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, data, mode)
}

func readJSONFile(path string, value any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, value); err != nil {
		return err
	}
	return nil
}

func validateRelativePath(path string) error {
	if path == "" {
		return errors.New("empty path")
	}
	if filepath.IsAbs(path) {
		return errors.New("absolute path is not allowed")
	}
	clean := filepath.Clean(path)
	if clean == "." || clean != path {
		return errors.New("path must be clean and relative")
	}
	for _, part := range strings.Split(path, string(filepath.Separator)) {
		if part == ".." {
			return errors.New("parent traversal is not allowed")
		}
	}
	return nil
}
