package main

import (
	"bytes"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	_ "modernc.org/sqlite"
)

// newRunner returns a helper that invokes Run against a fresh temporary
// database, plus the path to that database for direct state assertions.
func newRunner(t *testing.T) (run func(args ...string) (code int, stdout, stderr string), dbPath string) {
	t.Helper()
	dbPath = filepath.Join(t.TempDir(), "todos.db")
	run = func(args ...string) (int, string, string) {
		var out, errb bytes.Buffer
		code := Run(args, &out, &errb, dbPath)
		return code, out.String(), errb.String()
	}
	return run, dbPath
}

// dbRow mirrors the externally observable columns of a persisted Task.
type dbRow struct {
	ID     int64
	Title  string
	Status string
}

// dumpTasks reads the persisted Tasks back over a fresh connection, proving
// the rows survive independently of the process that wrote them.
func dumpTasks(t *testing.T, dbPath string) []dbRow {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	rows, err := db.Query("SELECT id, title, status FROM tasks ORDER BY id")
	if err != nil {
		t.Fatalf("query tasks: %v", err)
	}
	defer rows.Close()
	var out []dbRow
	for rows.Next() {
		var r dbRow
		if err := rows.Scan(&r.ID, &r.Title, &r.Status); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return out
}

func TestRun_AddAndList(t *testing.T) {
	run, dbPath := newRunner(t)

	if code, out, errs := run("add", "Buy milk"); code != 0 || !strings.Contains(out, "#1") || errs != "" {
		t.Fatalf("add 1: code=%d out=%q err=%q", code, out, errs)
	}
	if code, out, _ := run("add", "Pay rent"); code != 0 || !strings.Contains(out, "#2") {
		t.Fatalf("add 2: code=%d out=%q", code, out)
	}

	// Persisted rows, read over a separate connection, prove persistence
	// across invocations and the stable auto-increment ids in status Open.
	want := []dbRow{
		{ID: 1, Title: "Buy milk", Status: "open"},
		{ID: 2, Title: "Pay rent", Status: "open"},
	}
	if diff := cmp.Diff(want, dumpTasks(t, dbPath)); diff != "" {
		t.Errorf("persisted tasks mismatch (-want +got):\n%s", diff)
	}

	code, out, _ := run("list")
	if code != 0 {
		t.Fatalf("list: code=%d", code)
	}
	for _, sub := range []string{"#1", "Buy milk", "#2", "Pay rent"} {
		if !strings.Contains(out, sub) {
			t.Errorf("list output %q missing %q", out, sub)
		}
	}
}

func TestRun_LsAlias(t *testing.T) {
	run, _ := newRunner(t)
	run("add", "Task one")
	code, out, _ := run("ls")
	if code != 0 || !strings.Contains(out, "Task one") {
		t.Errorf("ls alias: code=%d out=%q", code, out)
	}
}

func TestRun_Help(t *testing.T) {
	run, _ := newRunner(t)
	code, out, _ := run("help")
	if code != 0 {
		t.Fatalf("help: code=%d", code)
	}
	if !strings.Contains(out, "Usage") {
		t.Errorf("help output missing usage text, got %q", out)
	}
}

func TestRun_Errors(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "no args", args: nil},
		{name: "unknown command", args: []string{"frobnicate"}},
		{name: "add without title", args: []string{"add"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			run, _ := newRunner(t)
			code, _, errs := run(tt.args...)
			if code == 0 {
				t.Errorf("expected non-zero exit code, got 0")
			}
			if errs == "" {
				t.Errorf("expected an error message on stderr")
			}
		})
	}
}
