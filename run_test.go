package main

import (
	"bytes"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// taskStatus reads a single Task's status and whether its completion time is
// set, over a fresh connection.
func taskStatus(t *testing.T, dbPath string, id int64) (status string, completedSet bool) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	var completed sql.NullString
	if err := db.QueryRow("SELECT status, completed_at FROM tasks WHERE id = ?", id).
		Scan(&status, &completed); err != nil {
		t.Fatalf("query task %d: %v", id, err)
	}
	return status, completed.Valid && completed.String != ""
}

func TestRun_Lifecycle(t *testing.T) {
	run, dbPath := newRunner(t)
	run("add", "Write report")

	if code, out, _ := run("start", "1"); code != 0 || !strings.Contains(out, "in-progress") {
		t.Fatalf("start: code=%d out=%q", code, out)
	}
	if st, completed := taskStatus(t, dbPath, 1); st != "in-progress" || completed {
		t.Errorf("after start: status=%q completed=%v, want in-progress / false", st, completed)
	}

	if code, _, _ := run("done", "1"); code != 0 {
		t.Fatalf("done: code=%d", code)
	}
	if st, completed := taskStatus(t, dbPath, 1); st != "done" || !completed {
		t.Errorf("after done: status=%q completed=%v, want done / true", st, completed)
	}

	// Default list hides Done; --all reveals it (Task stays on record).
	if _, out, _ := run("list"); strings.Contains(out, "Write report") {
		t.Errorf("default list should hide Done Task, got %q", out)
	}
	if _, out, _ := run("list", "--all"); !strings.Contains(out, "Write report") {
		t.Errorf("list --all should show Done Task, got %q", out)
	}

	// Reopen clears the completion time.
	if code, _, _ := run("reopen", "1"); code != 0 {
		t.Fatalf("reopen: code=%d", code)
	}
	if st, completed := taskStatus(t, dbPath, 1); st != "open" || completed {
		t.Errorf("after reopen: status=%q completed=%v, want open / false", st, completed)
	}
}

func TestRun_ListStatusFilter(t *testing.T) {
	run, _ := newRunner(t)
	run("add", "Alpha")   // 1: open
	run("add", "Bravo")   // 2: in-progress
	run("start", "2")
	run("add", "Charlie") // 3: done
	run("done", "3")

	if _, out, _ := run("list", "--status", "in-progress"); !strings.Contains(out, "Bravo") ||
		strings.Contains(out, "Alpha") || strings.Contains(out, "Charlie") {
		t.Errorf("--status in-progress = %q, want only Bravo", out)
	}
	if _, out, _ := run("list", "--status", "done"); !strings.Contains(out, "Charlie") ||
		strings.Contains(out, "Alpha") || strings.Contains(out, "Bravo") {
		t.Errorf("--status done = %q, want only Charlie", out)
	}
	if code, _, errs := run("list", "--status", "bogus"); code == 0 || errs == "" {
		t.Errorf("invalid status should error: code=%d errs=%q", code, errs)
	}
}

func TestRun_Show(t *testing.T) {
	run, _ := newRunner(t)
	run("add", "Inspect me")

	code, out, _ := run("show", "1")
	if code != 0 {
		t.Fatalf("show: code=%d", code)
	}
	for _, sub := range []string{"#1", "Inspect me", "open"} {
		if !strings.Contains(out, sub) {
			t.Errorf("show output %q missing %q", out, sub)
		}
	}

	run("done", "1")
	if _, out, _ := run("show", "1"); !strings.Contains(strings.ToLower(out), "completed") {
		t.Errorf("show after done should report completion, got %q", out)
	}
}

func TestRun_UnknownID(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "start", args: []string{"start", "999"}},
		{name: "done", args: []string{"done", "999"}},
		{name: "reopen", args: []string{"reopen", "999"}},
		{name: "show", args: []string{"show", "999"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			run, _ := newRunner(t)
			run("add", "exists") // id 1 exists; 999 does not
			code, _, errs := run(tt.args...)
			if code == 0 {
				t.Errorf("expected non-zero exit for unknown id")
			}
			if !strings.Contains(errs, "999") {
				t.Errorf("error message %q should mention the id", errs)
			}
		})
	}
}

// taskDue reads a single Task's stored due date over a fresh connection.
func taskDue(t *testing.T, dbPath string, id int64) (due string, set bool) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	var d sql.NullString
	if err := db.QueryRow("SELECT due FROM tasks WHERE id = ?", id).Scan(&d); err != nil {
		t.Fatalf("query due for %d: %v", id, err)
	}
	if !d.Valid || d.String == "" {
		return "", false
	}
	// The driver normalises DATE columns to RFC3339 on read; reduce to the day.
	if parsed, err := time.Parse(time.RFC3339, d.String); err == nil {
		return parsed.Format("2006-01-02"), true
	}
	return d.String, true
}

// inOrder reports whether each sub appears in haystack, in the given order.
func inOrder(haystack string, subs []string) bool {
	idx := 0
	for _, s := range subs {
		i := strings.Index(haystack[idx:], s)
		if i < 0 {
			return false
		}
		idx += i + len(s)
	}
	return true
}

func TestRun_DueDate(t *testing.T) {
	run, dbPath := newRunner(t)
	if code, _, errs := run("add", "Taxes", "--due", "2026-07-15"); code != 0 || errs != "" {
		t.Fatalf("add --due: code=%d errs=%q", code, errs)
	}
	if due, set := taskDue(t, dbPath, 1); !set || due != "2026-07-15" {
		t.Errorf("stored due = %q (set=%v), want 2026-07-15", due, set)
	}
	if _, out, _ := run("list"); !strings.Contains(out, "2026-07-15") {
		t.Errorf("list should display the due date, got %q", out)
	}
	if _, out, _ := run("show", "1"); !strings.Contains(out, "2026-07-15") {
		t.Errorf("show should display the due date, got %q", out)
	}
}

func TestRun_DueRelative(t *testing.T) {
	run, dbPath := newRunner(t)
	run("add", "Standup", "--due", "today")
	want := time.Now().Format("2006-01-02")
	if due, set := taskDue(t, dbPath, 1); !set || due != want {
		t.Errorf("due for 'today' = %q (set=%v), want %q", due, set, want)
	}
}

func TestRun_DueInvalid(t *testing.T) {
	run, dbPath := newRunner(t)
	code, _, errs := run("add", "Bad date", "--due", "someday")
	if code == 0 || errs == "" {
		t.Errorf("invalid due should error: code=%d errs=%q", code, errs)
	}
	if rows := dumpTasks(t, dbPath); len(rows) != 0 {
		t.Errorf("invalid due should create no Task, got %d rows", len(rows))
	}
}

func TestRun_DueOrdering(t *testing.T) {
	run, _ := newRunner(t)
	run("add", "Zeta")                         // 1: undated
	run("add", "Later", "--due", "2026-08-01") // 2
	run("add", "Soon", "--due", "2026-07-01")  // 3
	run("add", "Omega")                        // 4: undated

	// Soonest due first, undated last (ordered among themselves by id).
	want := []string{"Soon", "Later", "Zeta", "Omega"}
	_, out, _ := run("list")
	if !inOrder(out, want) {
		t.Errorf("list ordering wrong.\noutput:\n%s\nwant order: %v", out, want)
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
