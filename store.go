package main

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// errNoSuchTask is returned by store operations that reference an id with no
// matching Task row.
var errNoSuchTask = errors.New("no such task")

// Status is the lifecycle state of a Task. Transitions between any two
// statuses are free; setting Done stamps a completion time and leaving Done
// clears it.
type Status string

const (
	StatusOpen       Status = "open"
	StatusInProgress Status = "in-progress"
	StatusDone       Status = "done"
)

// Task is a single thing the user intends to do.
type Task struct {
	ID          int64
	Title       string
	Status      Status
	Due         *time.Time
	Notes       string
	Tags        []string
	CreatedAt   time.Time
	CompletedAt *time.Time
}

// ListFilter selects which Tasks ListTasks returns. The zero value lists only
// Open and In-progress Tasks.
type ListFilter struct {
	All    bool    // include Done Tasks
	Status *Status // restrict to a single status
	Tag    string  // restrict to Tasks carrying this Tag
}

// Store is the SQLite-backed persistence layer.
type Store struct{ db *sql.DB }

const (
	tsLayout  = time.RFC3339 // created_at / completed_at
	dayLayout = "2006-01-02"  // due (day granularity)
)

var schemaStmts = []string{
	`CREATE TABLE IF NOT EXISTS tasks (
		id           INTEGER PRIMARY KEY AUTOINCREMENT,
		title        TEXT NOT NULL,
		status       TEXT NOT NULL DEFAULT 'open',
		due          DATE NULL,
		notes        TEXT NOT NULL DEFAULT '',
		created_at   TIMESTAMP NOT NULL,
		completed_at TIMESTAMP NULL
	);`,
	`CREATE TABLE IF NOT EXISTS tags (
		id   INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL UNIQUE
	);`,
	`CREATE TABLE IF NOT EXISTS task_tags (
		task_id INTEGER NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
		tag_id  INTEGER NOT NULL REFERENCES tags(id),
		PRIMARY KEY (task_id, tag_id)
	);`,
}

// openStore opens (creating if necessary) the SQLite database at path,
// creating its parent directory and the schema idempotently.
func openStore(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("creating db directory: %w", err)
		}
	}
	// Enable foreign keys via the DSN so the pragma applies to every pooled
	// connection (a one-off PRAGMA would only affect a single connection),
	// which is what makes ON DELETE CASCADE reliable.
	dsn := path + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}
	for _, stmt := range schemaStmts {
		if _, err := db.Exec(stmt); err != nil {
			db.Close()
			return nil, fmt.Errorf("creating schema: %w", err)
		}
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// AddTask inserts a new Task in status Open with any due date, note, and Tags,
// and returns its stable id. Tags are get-or-created and linked.
func (s *Store) AddTask(title string, due *time.Time, notes string, tags []string, now time.Time) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	var dueVal any
	if due != nil {
		dueVal = due.Format(dayLayout)
	}
	res, err := tx.Exec(
		`INSERT INTO tasks(title, status, due, notes, created_at) VALUES (?, ?, ?, ?, ?)`,
		title, string(StatusOpen), dueVal, notes, now.UTC().Format(tsLayout),
	)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if err := linkTags(tx, id, tags); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return id, nil
}

// linkTags get-or-creates each Tag by name and links it to the Task. Blank
// names are skipped and re-using an existing name does not create a duplicate.
func linkTags(tx *sql.Tx, taskID int64, tags []string) error {
	for _, name := range tags {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, err := tx.Exec(`INSERT INTO tags(name) VALUES (?) ON CONFLICT(name) DO NOTHING`, name); err != nil {
			return err
		}
		var tagID int64
		if err := tx.QueryRow(`SELECT id FROM tags WHERE name = ?`, name).Scan(&tagID); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO task_tags(task_id, tag_id) VALUES (?, ?)`, taskID, tagID); err != nil {
			return err
		}
	}
	return nil
}

const taskColumns = `id, title, status, due, notes, created_at, completed_at`

// ListTasks returns Tasks matching the filter, ordered by id.
func (s *Store) ListTasks(f ListFilter) ([]Task, error) {
	q := `SELECT ` + taskColumns + ` FROM tasks`
	var where []string
	var args []any
	if f.Status != nil {
		where = append(where, "status = ?")
		args = append(args, string(*f.Status))
	} else if !f.All {
		where = append(where, "status IN ('open','in-progress')")
	}
	if f.Tag != "" {
		where = append(where,
			"id IN (SELECT tt.task_id FROM task_tags tt JOIN tags t ON t.id = tt.tag_id WHERE t.name = ?)")
		args = append(args, f.Tag)
	}
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	// Most urgent first: by due date (soonest first), undated last, then id.
	q += " ORDER BY due IS NULL, due ASC, id ASC"

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		tags, err := s.tagsFor(out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].Tags = tags
	}
	return out, nil
}

// tagsFor returns a Task's Tag names, ordered by name for stable display.
func (s *Store) tagsFor(taskID int64) ([]string, error) {
	rows, err := s.db.Query(
		`SELECT t.name FROM tags t JOIN task_tags tt ON tt.tag_id = t.id WHERE tt.task_id = ? ORDER BY t.name`,
		taskID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

// GetTask returns the Task with the given id, or errNoSuchTask if none exists.
func (s *Store) GetTask(id int64) (Task, error) {
	row := s.db.QueryRow(`SELECT `+taskColumns+` FROM tasks WHERE id = ?`, id)
	t, err := scanTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Task{}, errNoSuchTask
	}
	if err != nil {
		return Task{}, err
	}
	tags, err := s.tagsFor(id)
	if err != nil {
		return Task{}, err
	}
	t.Tags = tags
	return t, nil
}

// SetStatus moves a Task to the given status. Moving to Done stamps the
// completion time; moving to any other status clears it. Returns
// errNoSuchTask if the id does not exist.
func (s *Store) SetStatus(id int64, status Status, now time.Time) error {
	var completed any
	if status == StatusDone {
		completed = now.UTC().Format(tsLayout)
	}
	res, err := s.db.Exec(
		`UPDATE tasks SET status = ?, completed_at = ? WHERE id = ?`,
		string(status), completed, id,
	)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return errNoSuchTask
	}
	return nil
}

// parseStoredDay reads a due value back into a day-granularity time in UTC.
// The pure-Go SQLite driver normalises DATE columns to RFC3339 on read, so we
// accept both that and a bare YYYY-MM-DD, then collapse to the date.
func parseStoredDay(s string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339, dayLayout} {
		if d, err := time.Parse(layout, s); err == nil {
			return dayOf(d.UTC()), true
		}
	}
	return time.Time{}, false
}

type rowScanner interface{ Scan(dest ...any) error }

func scanTask(sc rowScanner) (Task, error) {
	var (
		id             int64
		title, status  string
		due, completed sql.NullString
		notes, created string
	)
	if err := sc.Scan(&id, &title, &status, &due, &notes, &created, &completed); err != nil {
		return Task{}, err
	}
	t := Task{ID: id, Title: title, Status: Status(status), Notes: notes}
	if due.Valid && due.String != "" {
		if d, ok := parseStoredDay(due.String); ok {
			t.Due = &d
		}
	}
	if c, err := time.Parse(tsLayout, created); err == nil {
		t.CreatedAt = c
	}
	if completed.Valid && completed.String != "" {
		if c, err := time.Parse(tsLayout, completed.String); err == nil {
			t.CompletedAt = &c
		}
	}
	return t, nil
}
