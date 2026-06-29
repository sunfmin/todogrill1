# Use SQLite (pure-Go driver) for local storage

This is a single-user local CLI todo tool. We store todos in a local SQLite
database rather than a JSON file or plain text, so we get structured fields,
transactional writes, and room to add querying/filtering later. We use the
pure-Go driver `modernc.org/sqlite` (not the CGO-based `mattn/go-sqlite3`) so the
tool still compiles to a single zero-dependency static binary and cross-compiles
without a C toolchain — which was a primary reason we chose Go.

## Considered Options

- **Single JSON file** — simplest, but no transactions and awkward as fields grow.
- **Plain text (todo.txt)** — greppable but can't cleanly hold structured fields (status, dates).
- **SQLite + `mattn/go-sqlite3` (CGO)** — most mature driver, but CGO breaks the static-binary / easy-cross-compile story.

## Consequences

- DB file lives at `~/.config/todo/todos.db` (XDG; falls back to `~/.todo.db`).
- `modernc.org/sqlite` is less battle-tested and slightly slower than the CGO driver, which is irrelevant at todo-list scale.
