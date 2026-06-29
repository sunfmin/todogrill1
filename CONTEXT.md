# Todo

A single-user, command-line tool for tracking personal tasks, backed by a local SQLite database.

## Language

**Task**:
A single thing the user intends to do, tracked as one entry in the tool.
_Avoid_: Todo (that's the tool/command), Item, Entry

## Status

A Task is always in exactly one of three statuses.

**Open**:
A Task that has been created but not yet started. The status every Task starts in.
_Avoid_: Pending, Todo, New

**In-progress**:
A Task the user has actively started working on.
_Avoid_: Doing, Active, WIP, Started

**Done**:
A Task the user has finished. Kept on record rather than removed.
_Avoid_: Complete, Finished, Closed, Resolved

## Other terms

**Tag**:
A free-form label attached to a Task to group or filter it. A Tag comes into existence the moment it is first attached to a Task; there is no separate step to create or manage Tags. A Task may have many Tags.
_Avoid_: Label, Category, Project, List
