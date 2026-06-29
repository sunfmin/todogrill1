package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
)

const usageText = `todo — a single-user CLI task tracker

Usage:
  todo <command> [arguments]

Commands:
  add <title>   Add a new Task (status Open)
  list, ls      List Tasks (default: Open and In-progress)
  show <id>     Show a Task's full detail
  start <id>    Mark a Task In-progress
  done <id>     Mark a Task Done
  reopen <id>   Reopen a Task (back to Open)
  edit <id>     Change a Task's title, due date, Tags, or note
  rm <id>       Delete a Task
  help          Show this help

Run "todo <command> -h" for command-specific help.
`

func printUsage(w io.Writer) { fmt.Fprint(w, usageText) }

// stringSlice is a flag.Value that accumulates repeated occurrences of a flag,
// e.g. --tag home --tag urgent.
type stringSlice []string

func (s *stringSlice) String() string { return strings.Join(*s, ",") }
func (s *stringSlice) Set(v string) error {
	*s = append(*s, v)
	return nil
}

// Run is the single entry seam for the CLI. It executes one command and
// returns the process exit code: 0 on success, non-zero on failure.
func Run(args []string, stdout, stderr io.Writer, dbPath string) int {
	if len(args) == 0 {
		printUsage(stderr)
		return 2
	}
	switch args[0] {
	case "-h", "--help", "help":
		printUsage(stdout)
		return 0
	}

	st, err := openStore(dbPath)
	if err != nil {
		fmt.Fprintf(stderr, "todo: %v\n", err)
		return 1
	}
	defer st.Close()

	now := time.Now()
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "add":
		return cmdAdd(st, rest, stdout, stderr, now)
	case "list", "ls":
		return cmdList(st, rest, stdout, stderr)
	case "show":
		return cmdShow(st, rest, stdout, stderr)
	case "start":
		return cmdSetStatus(st, rest, stdout, stderr, "start", StatusInProgress, now)
	case "done":
		return cmdSetStatus(st, rest, stdout, stderr, "done", StatusDone, now)
	case "reopen":
		return cmdSetStatus(st, rest, stdout, stderr, "reopen", StatusOpen, now)
	case "edit":
		return cmdEdit(st, rest, stdout, stderr, now)
	case "rm":
		return cmdRm(st, rest, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "todo: unknown command %q\n\n", cmd)
		printUsage(stderr)
		return 2
	}
}

func cmdAdd(st *Store, args []string, stdout, stderr io.Writer, now time.Time) int {
	fs := flag.NewFlagSet("add", flag.ContinueOnError)
	fs.SetOutput(stderr)
	due := fs.String("due", "", "due date: YYYY-MM-DD, today, or tomorrow")
	note := fs.String("note", "", "free-form note")
	var tags stringSlice
	fs.Var(&tags, "tag", "Tag to attach (repeatable)")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: todo add <title> [--due <date>] [--tag <name> ...] [--note <text>]")
		fs.PrintDefaults()
	}
	rest, err := parseFlagsPermuted(fs, args)
	if err != nil {
		return 2
	}
	title := strings.TrimSpace(strings.Join(rest, " "))
	if title == "" {
		fmt.Fprintln(stderr, "todo: add requires a title")
		return 2
	}

	var duePtr *time.Time
	if *due != "" {
		d, err := parseDue(*due, now)
		if err != nil {
			fmt.Fprintf(stderr, "todo: %v\n", err)
			return 2
		}
		duePtr = &d
	}

	id, err := st.AddTask(title, duePtr, *note, tags, now)
	if err != nil {
		fmt.Fprintf(stderr, "todo: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "Added task #%d: %s\n", id, title)
	return 0
}

func cmdList(st *Store, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	all := fs.Bool("all", false, "include Done Tasks")
	status := fs.String("status", "", "filter by status: open, in-progress, or done")
	tag := fs.String("tag", "", "filter by Tag")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: todo list [--all] [--status open|in-progress|done] [--tag <name>]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}

	filter := ListFilter{All: *all, Tag: *tag}
	if *status != "" {
		s, ok := parseStatus(*status)
		if !ok {
			fmt.Fprintf(stderr, "todo: invalid status %q (want open, in-progress, or done)\n", *status)
			return 2
		}
		filter.Status = &s
	}

	tasks, err := st.ListTasks(filter)
	if err != nil {
		fmt.Fprintf(stderr, "todo: %v\n", err)
		return 1
	}
	if len(tasks) == 0 {
		fmt.Fprintln(stdout, "No tasks.")
		return 0
	}

	w := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	for _, t := range tasks {
		fmt.Fprintf(w, "#%d\t%s\t%s\n", t.ID, t.Status, taskSummary(t))
	}
	if err := w.Flush(); err != nil {
		fmt.Fprintf(stderr, "todo: %v\n", err)
		return 1
	}
	return 0
}

func cmdShow(st *Store, args []string, stdout, stderr io.Writer) int {
	id, ok := parseSingleID("show", args, stderr)
	if !ok {
		return 2
	}
	t, err := st.GetTask(id)
	if err != nil {
		return reportTaskErr(stderr, id, err)
	}
	writeDetail(stdout, t)
	return 0
}

// cmdSetStatus backs start/done/reopen: it moves a Task to status, with name
// used in usage and messages.
func cmdSetStatus(st *Store, args []string, stdout, stderr io.Writer, name string, status Status, now time.Time) int {
	id, ok := parseSingleID(name, args, stderr)
	if !ok {
		return 2
	}
	if err := st.SetStatus(id, status, now); err != nil {
		return reportTaskErr(stderr, id, err)
	}
	fmt.Fprintf(stdout, "Task #%d → %s\n", id, status)
	return 0
}

func cmdEdit(st *Store, args []string, stdout, stderr io.Writer, now time.Time) int {
	fs := flag.NewFlagSet("edit", flag.ContinueOnError)
	fs.SetOutput(stderr)
	title := fs.String("title", "", "new title")
	due := fs.String("due", "", "new due date: YYYY-MM-DD, today, or tomorrow")
	clearDue := fs.Bool("clear-due", false, "remove the due date")
	note := fs.String("note", "", "new note")
	var addTags, rmTags stringSlice
	fs.Var(&addTags, "add-tag", "Tag to add (repeatable)")
	fs.Var(&rmTags, "rm-tag", "Tag to remove (repeatable)")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: todo edit <id> [--title <text>] [--due <date> | --clear-due] [--add-tag <name> ...] [--rm-tag <name> ...] [--note <text>]")
		fs.PrintDefaults()
	}

	rest, err := parseFlagsPermuted(fs, args)
	if err != nil {
		return 2
	}
	if len(rest) != 1 {
		fmt.Fprintln(stderr, "todo: edit requires a single task id")
		return 2
	}
	id, err := strconv.ParseInt(rest[0], 10, 64)
	if err != nil {
		fmt.Fprintf(stderr, "todo: invalid task id %q\n", rest[0])
		return 2
	}

	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	if !set["title"] && !set["due"] && !*clearDue && !set["note"] &&
		len(addTags) == 0 && len(rmTags) == 0 {
		fmt.Fprintln(stderr, "todo: edit needs at least one change (--title, --due, --clear-due, --add-tag, --rm-tag, --note)")
		return 2
	}
	if set["due"] && *clearDue {
		fmt.Fprintln(stderr, "todo: --due and --clear-due cannot be used together")
		return 2
	}

	var e TaskEdit
	if set["title"] {
		if strings.TrimSpace(*title) == "" {
			fmt.Fprintln(stderr, "todo: title cannot be empty")
			return 2
		}
		e.Title = title
	}
	e.ClearDue = *clearDue
	if set["due"] {
		d, err := parseDue(*due, now)
		if err != nil {
			fmt.Fprintf(stderr, "todo: %v\n", err)
			return 2
		}
		e.Due = &d
	}
	if set["note"] {
		e.Notes = note
	}
	e.AddTags = addTags
	e.RmTags = rmTags

	if err := st.EditTask(id, e); err != nil {
		return reportTaskErr(stderr, id, err)
	}
	fmt.Fprintf(stdout, "Updated task #%d\n", id)
	return 0
}

func cmdRm(st *Store, args []string, stdout, stderr io.Writer) int {
	id, ok := parseSingleID("rm", args, stderr)
	if !ok {
		return 2
	}
	if err := st.DeleteTask(id); err != nil {
		return reportTaskErr(stderr, id, err)
	}
	fmt.Fprintf(stdout, "Deleted task #%d\n", id)
	return 0
}

// parseFlagsPermuted parses fs allowing flags to appear before or after the
// positional arguments, unlike flag.FlagSet.Parse which stops at the first
// non-flag token. It returns the collected positional arguments.
func parseFlagsPermuted(fs *flag.FlagSet, args []string) ([]string, error) {
	var positionals []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		rest := fs.Args()
		if len(rest) == 0 {
			return positionals, nil
		}
		positionals = append(positionals, rest[0])
		args = rest[1:]
	}
}

// parseSingleID parses the single <id> argument shared by show/start/done/
// reopen/edit/rm. It reports usage errors to stderr and returns ok=false.
func parseSingleID(name string, args []string, stderr io.Writer) (int64, bool) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprintf(stderr, "Usage: todo %s <id>\n", name) }
	if err := fs.Parse(args); err != nil {
		return 0, false
	}
	if fs.NArg() != 1 {
		fmt.Fprintf(stderr, "todo: %s requires a single task id\n", name)
		return 0, false
	}
	id, err := strconv.ParseInt(fs.Arg(0), 10, 64)
	if err != nil {
		fmt.Fprintf(stderr, "todo: invalid task id %q\n", fs.Arg(0))
		return 0, false
	}
	return id, true
}

// reportTaskErr turns a store error into a user-facing message and exit code,
// giving a clear message when the id does not exist.
func reportTaskErr(stderr io.Writer, id int64, err error) int {
	if errors.Is(err, errNoSuchTask) {
		fmt.Fprintf(stderr, "todo: no task with id %d\n", id)
		return 1
	}
	fmt.Fprintf(stderr, "todo: %v\n", err)
	return 1
}

func parseStatus(s string) (Status, bool) {
	switch Status(s) {
	case StatusOpen, StatusInProgress, StatusDone:
		return Status(s), true
	}
	return "", false
}

// writeDetail prints a Task's full detail. Optional fields (due, tags, note,
// completion) appear only when set, so a Task's detail is stable as later
// features come online.
func writeDetail(w io.Writer, t Task) {
	fmt.Fprintf(w, "Task #%d\n", t.ID)
	fmt.Fprintf(w, "  Title:     %s\n", t.Title)
	fmt.Fprintf(w, "  Status:    %s\n", t.Status)
	if t.Due != nil {
		fmt.Fprintf(w, "  Due:       %s\n", t.Due.Format(dayLayout))
	}
	if len(t.Tags) > 0 {
		fmt.Fprintf(w, "  Tags:      %s\n", strings.Join(t.Tags, ", "))
	}
	if t.Notes != "" {
		fmt.Fprintf(w, "  Note:      %s\n", t.Notes)
	}
	fmt.Fprintf(w, "  Created:   %s\n", t.CreatedAt.Local().Format("2006-01-02 15:04"))
	if t.CompletedAt != nil {
		fmt.Fprintf(w, "  Done at:   %s\n", t.CompletedAt.Local().Format("2006-01-02 15:04"))
	}
}

// taskSummary renders a Task on one line: the title, followed by an optional
// due-date suffix and optional Tag suffixes. A Task with neither renders as
// just its title, so its appearance is stable as later features come online.
func taskSummary(t Task) string {
	var b strings.Builder
	b.WriteString(t.Title)
	if t.Due != nil {
		fmt.Fprintf(&b, " (due %s)", t.Due.Format(dayLayout))
	}
	if len(t.Tags) > 0 {
		parts := make([]string, len(t.Tags))
		for i, tag := range t.Tags {
			parts[i] = "#" + tag
		}
		b.WriteString(" ")
		b.WriteString(strings.Join(parts, " "))
	}
	return b.String()
}
