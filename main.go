package main

import (
	"os"
	"path/filepath"
)

func main() {
	os.Exit(Run(os.Args[1:], os.Stdout, os.Stderr, defaultDBPath()))
}

// defaultDBPath resolves the on-disk location of the database from the XDG
// config directory ($XDG_CONFIG_HOME/todo/todos.db, defaulting to
// ~/.config/todo/todos.db), falling back to ~/.todo.db when the home
// directory cannot be determined. Tests always pass an explicit path instead.
func defaultDBPath() string {
	if base := os.Getenv("XDG_CONFIG_HOME"); base != "" {
		return filepath.Join(base, "todo", "todos.db")
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".config", "todo", "todos.db")
	}
	return ".todo.db"
}
