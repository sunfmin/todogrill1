package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

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

const schema = `
CREATE TABLE IF NOT EXISTS tasks (
	id           INTEGER PRIMARY KEY AUTOINCREMENT,
	title        TEXT NOT NULL,
	status       TEXT NOT NULL DEFAULT 'open',
	due          DATE NULL,
	notes        TEXT NOT NULL DEFAULT '',
	created_at   TIMESTAMP NOT NULL,
	completed_at TIMESTAMP NULL
);`

// openStore opens (creating if necessary) the SQLite database at path,
// creating its parent directory and the schema idempotently.
func openStore(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("creating db directory: %w", err)
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}
	if _, err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		db.Close()
		return nil, fmt.Errorf("enabling foreign keys: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("creating schema: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// AddTask inserts a new Task in status Open and returns its stable id.
func (s *Store) AddTask(title string, due *time.Time, notes string, tags []string, now time.Time) (int64, error) {
	var dueVal any
	if due != nil {
		dueVal = due.Format(dayLayout)
	}
	res, err := s.db.Exec(
		`INSERT INTO tasks(title, status, due, notes, created_at) VALUES (?, ?, ?, ?, ?)`,
		title, string(StatusOpen), dueVal, notes, now.UTC().Format(tsLayout),
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

const taskColumns = `id, title, status, due, notes, created_at, completed_at`

// ListTasks returns Tasks matching the filter, ordered by id.
func (s *Store) ListTasks(f ListFilter) ([]Task, error) {
	q := `SELECT ` + taskColumns + ` FROM tasks`
	var where []string
	var args []any
	if !f.All {
		where = append(where, "status IN ('open','in-progress')")
	}
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY id"

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
	return out, rows.Err()
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
		if d, err := time.Parse(dayLayout, due.String); err == nil {
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
