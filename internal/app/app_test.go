package app

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUpdateProviderInLinePreservesOtherContent(t *testing.T) {
	line := []byte(`{"x":{"model_provider":"nested"},"model_provider":"old","z":[1,{"model_provider":"nested"}]}`)

	got, changed, err := updateProviderInLine(line, "openai")
	if err != nil {
		t.Fatal(err)
	}
	if changed != 1 {
		t.Fatal("expected provider to change")
	}
	want := `{"x":{"model_provider":"nested"},"model_provider":"openai","z":[1,{"model_provider":"nested"}]}`
	if string(got) != want {
		t.Fatalf("unexpected rewrite:\nwant %s\n got %s", want, got)
	}
}

func TestUpdateProviderInLineLeavesMissingProviderAlone(t *testing.T) {
	line := []byte(`{"x":{"model_provider":"nested"}}`)

	got, changed, err := updateProviderInLine(line, "openai")
	if err != nil {
		t.Fatal(err)
	}
	if changed != 0 {
		t.Fatal("expected no change")
	}
	if string(got) != string(line) {
		t.Fatalf("line changed unexpectedly: %s", got)
	}
}

func TestUpdateProviderInLineUpdatesNestedPayload(t *testing.T) {
	line := []byte(`{"timestamp":"2026-05-09T16:30:48.405Z","type":"session_meta","payload":{"id":"abc","model_provider":"custom","text":"model_provider should not change"}}`)

	got, changed, err := updateProviderInLine(line, "openai")
	if err != nil {
		t.Fatal(err)
	}
	if changed != 1 {
		t.Fatalf("changed=%d, want 1", changed)
	}
	want := `{"timestamp":"2026-05-09T16:30:48.405Z","type":"session_meta","payload":{"id":"abc","model_provider":"openai","text":"model_provider should not change"}}`
	if string(got) != want {
		t.Fatalf("unexpected rewrite:\nwant %s\n got %s", want, got)
	}
	values, err := stringFieldValues(got, targetField)
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 1 || values[0] != "openai" {
		t.Fatalf("values=%v", values)
	}
}

func TestParseModelProviderFromTOML(t *testing.T) {
	data := []byte(`
# model_provider = "ignored"
model = "gpt"
model_provider = "zed" # comment
`)
	provider, ok, err := parseModelProviderFromTOML(data)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || provider != "zed" {
		t.Fatalf("provider=%q ok=%v", provider, ok)
	}
}

func TestBackupRevertRestoreAndStatus(t *testing.T) {
	home := t.TempDir()
	app := testApp(home)
	writeFile(t, filepath.Join(home, ".codex", "config.toml"), `model_provider = "target"`+"\n")
	session := filepath.Join(home, ".codex", "sessions", "2026", "05", "10", "session.jsonl")
	original := `{"model_provider":"old","keep":1}` + "\n" + `{"keep":{"model_provider":"nested"}}` + "\n"
	writeFile(t, session, original)

	var out bytes.Buffer
	app.Out = &out
	if err := app.Run([]string{"status"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Session files: 1") || !strings.Contains(out.String(), "- old: 1") {
		t.Fatalf("unexpected status output:\n%s", out.String())
	}

	out.Reset()
	if err := app.Run([]string{"revert"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Backup: csr-20260510-010203") {
		t.Fatalf("backup name missing:\n%s", out.String())
	}
	changed, err := os.ReadFile(session)
	if err != nil {
		t.Fatal(err)
	}
	wantChanged := `{"model_provider":"target","keep":1}` + "\n" + `{"keep":{"model_provider":"nested"}}` + "\n"
	if string(changed) != wantChanged {
		t.Fatalf("unexpected changed file:\nwant %q\n got %q", wantChanged, changed)
	}

	out.Reset()
	if err := app.Run([]string{"restore", "20260510"}); err != nil {
		t.Fatal(err)
	}
	restored, err := os.ReadFile(session)
	if err != nil {
		t.Fatal(err)
	}
	if string(restored) != original {
		t.Fatalf("restore failed:\nwant %q\n got %q", original, restored)
	}
	if !strings.Contains(out.String(), "Files restored: 1") {
		t.Fatalf("unexpected restore output:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "Restored backup: csr-20260510-010203") {
		t.Fatalf("restore should print resolved backup name:\n%s", out.String())
	}
}

func TestRevertBacksUpUpdatesAndRestoresSQLiteState(t *testing.T) {
	requireSQLite(t)
	home := t.TempDir()
	app := testApp(home)
	writeFile(t, filepath.Join(home, ".codex", "config.toml"), `model_provider = "target"`+"\n")
	session := filepath.Join(home, ".codex", "sessions", "2026", "05", "10", "session.jsonl")
	writeFile(t, session, `{"model_provider":"old"}`+"\n")
	createStateDB(t, app.StatePath(), map[string]string{"thread-1": "old", "thread-2": "target"})

	var out bytes.Buffer
	app.Out = &out
	if err := app.Run([]string{"status"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "SQLite threads: 2") || !strings.Contains(out.String(), "- old: 1") {
		t.Fatalf("unexpected status output:\n%s", out.String())
	}

	out.Reset()
	if err := app.Run([]string{"revert"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "model_provider fields updated: 2") {
		t.Fatalf("unexpected revert output:\n%s", out.String())
	}
	if got := stateProviders(t, app.StatePath()); got != "target:2" {
		t.Fatalf("sqlite providers=%s", got)
	}

	out.Reset()
	if err := app.Run([]string{"restore", "20260510"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Files restored: 2") {
		t.Fatalf("unexpected restore output:\n%s", out.String())
	}
	if got := stateProviders(t, app.StatePath()); got != "old:1,target:1" {
		t.Fatalf("restored sqlite providers=%s", got)
	}
}

func TestRestoreAcceptsUniquePrefixAndDetectsAmbiguity(t *testing.T) {
	home := t.TempDir()
	app := testApp(home)
	session := filepath.Join(home, ".codex", "sessions", "2026", "05", "10", "session.jsonl")
	writeFile(t, session, `{"model_provider":"old"}`+"\n")

	name, _, err := app.Backup()
	if err != nil {
		t.Fatal(err)
	}
	if name != "csr-20260510-010203" {
		t.Fatalf("name=%q", name)
	}
	resolved, err := app.ResolveBackupName("20260510")
	if err != nil {
		t.Fatal(err)
	}
	if resolved != name {
		t.Fatalf("resolved=%q want %q", resolved, name)
	}

	second := filepath.Join(app.BackupRoot(), "csr-20260510-010204")
	if err := os.MkdirAll(second, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := app.ResolveBackupName("20260510"); err == nil {
		t.Fatal("expected ambiguous prefix error")
	}
}

func TestRevertAbortsOnInvalidJSONAfterBackup(t *testing.T) {
	home := t.TempDir()
	app := testApp(home)
	session := filepath.Join(home, ".codex", "sessions", "2026", "05", "10", "bad.jsonl")
	original := `{"model_provider":"old"}` + "\n" + `not-json` + "\n"
	writeFile(t, session, original)

	err := app.Run([]string{"revert", "--provider", "openai"})
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(err.Error(), "Backup already created") {
		t.Fatalf("expected backup guidance, got %v", err)
	}
	data, readErr := os.ReadFile(session)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(data) != original {
		t.Fatalf("invalid file should not be modified:\nwant %q\n got %q", original, data)
	}
	backups, globErr := filepath.Glob(filepath.Join(home, ".codex", "session-provider-backups", backupPrefix+"*"))
	if globErr != nil {
		t.Fatal(globErr)
	}
	if len(backups) != 1 {
		t.Fatalf("expected one backup, got %d", len(backups))
	}
}

func testApp(home string) *App {
	return &App{
		Home: home,
		Now: func() time.Time {
			return time.Date(2026, 5, 10, 1, 2, 3, 0, time.UTC)
		},
		Out:     &bytes.Buffer{},
		Err:     &bytes.Buffer{},
		Workers: 2,
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func requireSQLite(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 is not available")
	}
}

func createStateDB(t *testing.T, path string, rows map[string]string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := runTestSQLite(path, `CREATE TABLE threads (id TEXT PRIMARY KEY, model_provider TEXT NOT NULL);`); err != nil {
		t.Fatal(err)
	}
	for id, provider := range rows {
		sql := `INSERT INTO threads (id, model_provider) VALUES (` + sqlString(id) + `, ` + sqlString(provider) + `);`
		if err := runTestSQLite(path, sql); err != nil {
			t.Fatal(err)
		}
	}
}

func stateProviders(t *testing.T, path string) string {
	t.Helper()
	cmd := exec.Command("sqlite3", "-batch", path, `SELECT model_provider || ':' || COUNT(*) FROM threads GROUP BY model_provider ORDER BY model_provider;`)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("sqlite3 query failed: %v: %s", err, out)
	}
	return strings.Join(nonEmptyLines(string(out)), ",")
}

func runTestSQLite(path, sql string) error {
	cmd := exec.Command("sqlite3", "-batch", path, sql)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return errWithOutput(err, out)
	}
	return nil
}

func errWithOutput(err error, out []byte) error {
	if len(out) == 0 {
		return err
	}
	return &sqliteTestError{err: err, out: strings.TrimSpace(string(out))}
}

type sqliteTestError struct {
	err error
	out string
}

func (e *sqliteTestError) Error() string {
	return e.err.Error() + ": " + e.out
}
