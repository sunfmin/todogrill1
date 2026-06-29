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

// taskTags reads a Task's linked Tag names over a fresh connection.
func taskTags(t *testing.T, dbPath string, id int64) []string {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	rows, err := db.Query(
		`SELECT t.name FROM tags t JOIN task_tags tt ON tt.tag_id = t.id WHERE tt.task_id = ? ORDER BY t.name`, id)
	if err != nil {
		t.Fatalf("query tags: %v", err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatalf("scan: %v", err)
		}
		names = append(names, n)
	}
	return names
}

// countTagRows counts rows in the tags table with the given name.
func countTagRows(t *testing.T, dbPath, name string) int {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM tags WHERE name = ?`, name).Scan(&n); err != nil {
		t.Fatalf("count tags: %v", err)
	}
	return n
}

func TestRun_Tags(t *testing.T) {
	run, dbPath := newRunner(t)
	if code, _, errs := run("add", "Mow lawn", "--tag", "home", "--tag", "weekend"); code != 0 || errs != "" {
		t.Fatalf("add --tag: code=%d errs=%q", code, errs)
	}
	if diff := cmp.Diff([]string{"home", "weekend"}, taskTags(t, dbPath, 1)); diff != "" {
		t.Errorf("linked tags mismatch (-want +got):\n%s", diff)
	}
	if _, out, _ := run("list"); !strings.Contains(out, "#home") || !strings.Contains(out, "#weekend") {
		t.Errorf("list should display tags, got %q", out)
	}
	if _, out, _ := run("show", "1"); !strings.Contains(out, "home") || !strings.Contains(out, "weekend") {
		t.Errorf("show should display tags, got %q", out)
	}
}

func TestRun_TagReuseNoDuplicate(t *testing.T) {
	run, dbPath := newRunner(t)
	run("add", "First", "--tag", "errand")
	run("add", "Second", "--tag", "errand")

	if n := countTagRows(t, dbPath, "errand"); n != 1 {
		t.Errorf("tag 'errand' row count = %d, want 1 (reused, not duplicated)", n)
	}
	if diff := cmp.Diff([]string{"errand"}, taskTags(t, dbPath, 2)); diff != "" {
		t.Errorf("task 2 tags mismatch (-want +got):\n%s", diff)
	}
}

func TestRun_ListTagFilter(t *testing.T) {
	run, _ := newRunner(t)
	run("add", "Groceries", "--tag", "home")
	run("add", "Deploy", "--tag", "work")
	run("add", "Laundry", "--tag", "home")

	_, out, _ := run("list", "--tag", "home")
	if !strings.Contains(out, "Groceries") || !strings.Contains(out, "Laundry") {
		t.Errorf("list --tag home should include both home Tasks, got %q", out)
	}
	if strings.Contains(out, "Deploy") {
		t.Errorf("list --tag home should exclude the work Task, got %q", out)
	}
}

// taskRow reads a Task's title and notes; exists reports whether the row is
// present at all.
func taskRow(t *testing.T, dbPath string, id int64) (title, notes string, exists bool) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	err = db.QueryRow("SELECT title, notes FROM tasks WHERE id = ?", id).Scan(&title, &notes)
	if err == sql.ErrNoRows {
		return "", "", false
	}
	if err != nil {
		t.Fatalf("query task %d: %v", id, err)
	}
	return title, notes, true
}

// countTaskTags counts a Task's task_tags link rows.
func countTaskTags(t *testing.T, dbPath string, taskID int64) int {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow("SELECT count(*) FROM task_tags WHERE task_id = ?", taskID).Scan(&n); err != nil {
		t.Fatalf("count task_tags: %v", err)
	}
	return n
}

func TestRun_Note(t *testing.T) {
	run, dbPath := newRunner(t)
	run("add", "Call plumber", "--note", "leak under sink")

	if _, notes, _ := taskRow(t, dbPath, 1); notes != "leak under sink" {
		t.Errorf("stored note = %q, want %q", notes, "leak under sink")
	}
	if _, out, _ := run("show", "1"); !strings.Contains(out, "leak under sink") {
		t.Errorf("show should display the note, got %q", out)
	}

	run("edit", "1", "--note", "fixed, monitor for a week")
	if _, notes, _ := taskRow(t, dbPath, 1); notes != "fixed, monitor for a week" {
		t.Errorf("after edit --note, note = %q", notes)
	}
}

func TestRun_EditTitle(t *testing.T) {
	run, dbPath := newRunner(t)
	run("add", "Bye milk") // typo
	if code, _, errs := run("edit", "1", "--title", "Buy milk"); code != 0 || errs != "" {
		t.Fatalf("edit --title: code=%d errs=%q", code, errs)
	}
	if title, _, _ := taskRow(t, dbPath, 1); title != "Buy milk" {
		t.Errorf("title after edit = %q, want %q", title, "Buy milk")
	}
}

func TestRun_EditDueSetAndClear(t *testing.T) {
	run, dbPath := newRunner(t)
	run("add", "Renew passport", "--due", "2026-07-01")

	run("edit", "1", "--due", "2026-09-30")
	if due, set := taskDue(t, dbPath, 1); !set || due != "2026-09-30" {
		t.Errorf("after edit --due, due = %q (set=%v), want 2026-09-30", due, set)
	}

	run("edit", "1", "--clear-due")
	if due, set := taskDue(t, dbPath, 1); set {
		t.Errorf("after --clear-due, due = %q (set=%v), want cleared", due, set)
	}

	if code, _, errs := run("edit", "1", "--due", "2026-01-01", "--clear-due"); code == 0 || errs == "" {
		t.Errorf("--due with --clear-due should error: code=%d errs=%q", code, errs)
	}
}

func TestRun_EditTags(t *testing.T) {
	run, dbPath := newRunner(t)
	run("add", "Plan trip", "--tag", "old")
	run("edit", "1", "--add-tag", "travel", "--add-tag", "fun", "--rm-tag", "old")

	if diff := cmp.Diff([]string{"fun", "travel"}, taskTags(t, dbPath, 1)); diff != "" {
		t.Errorf("tags after edit mismatch (-want +got):\n%s", diff)
	}
}

func TestRun_RmCascadeAndIDStable(t *testing.T) {
	run, dbPath := newRunner(t)
	run("add", "First", "--tag", "x") // id 1
	run("add", "Second")              // id 2

	if code, _, errs := run("rm", "1"); code != 0 || errs != "" {
		t.Fatalf("rm: code=%d errs=%q", code, errs)
	}
	if _, _, exists := taskRow(t, dbPath, 1); exists {
		t.Errorf("task 1 should be gone after rm")
	}
	if n := countTaskTags(t, dbPath, 1); n != 0 {
		t.Errorf("task 1 tag links should cascade away, got %d", n)
	}
	if _, out, _ := run("list", "--all"); strings.Contains(out, "First") {
		t.Errorf("deleted Task should not appear in any listing, got %q", out)
	}

	// The retired id is not reused: the next add gets id 3, not 1.
	run("add", "Third")
	if title, _, exists := taskRow(t, dbPath, 3); !exists || title != "Third" {
		t.Errorf("next add should take id 3 (no reuse), got title=%q exists=%v", title, exists)
	}
	if _, _, exists := taskRow(t, dbPath, 1); exists {
		t.Errorf("id 1 must stay retired, but a row reappeared at id 1")
	}
}

func TestRun_EditRmUnknownID(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "edit", args: []string{"edit", "999", "--title", "x"}},
		{name: "rm", args: []string{"rm", "999"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			run, _ := newRunner(t)
			run("add", "exists")
			code, _, errs := run(tt.args...)
			if code == 0 {
				t.Errorf("expected non-zero exit for unknown id")
			}
			if !strings.Contains(errs, "999") {
				t.Errorf("error %q should mention the id", errs)
			}
		})
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
