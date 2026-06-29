package main

import (
	"flag"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"
)

const usageText = `todo — a single-user CLI task tracker

Usage:
  todo <command> [arguments]

Commands:
  add <title>   Add a new Task (status Open)
  list, ls      List Open and In-progress Tasks
  help          Show this help

Run "todo <command> -h" for command-specific help.
`

func printUsage(w io.Writer) { fmt.Fprint(w, usageText) }

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
	default:
		fmt.Fprintf(stderr, "todo: unknown command %q\n\n", cmd)
		printUsage(stderr)
		return 2
	}
}

func cmdAdd(st *Store, args []string, stdout, stderr io.Writer, now time.Time) int {
	fs := flag.NewFlagSet("add", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: todo add <title>")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	title := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if title == "" {
		fmt.Fprintln(stderr, "todo: add requires a title")
		return 2
	}
	id, err := st.AddTask(title, nil, "", nil, now)
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
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: todo list [--all]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}

	tasks, err := st.ListTasks(ListFilter{All: *all})
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
