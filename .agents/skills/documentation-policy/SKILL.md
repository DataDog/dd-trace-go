---
name: documentation-policy
description: Use when adding or reviewing significant features, make options or commands, CI workflows, or scoped AGENTS.md files.
user-invocable: false
metadata:
  internal: true
---

## Updating Documentation

The root [AGENTS.md](/AGENTS.md) should be short. Only update it if a new scoped `AGENTS.md` file is added, so it can be added to the list with its purpose.

Document changes in the nearest existing doc: the package's [Go doc comment](https://go.dev/doc/comment) or `doc.go` for API/behavior, the closest `README.md` (e.g. [`contrib/README.md`](/contrib/README.md)) for an area, or [`FAQ.md`](/FAQ.md)/[`MIGRATING.md`](/MIGRATING.md) for user-facing notes.

The developer should update [`CONTRIBUTING.md`](/CONTRIBUTING.md) with new, significant features. A feature is significant when:

1. It introduces a new method of interacting with and/or customizing the tracer (ie new scripts for generating files, options for configuration sources, etc)
2. A new internal functionality is introduced that can replace a common, built-in Go library
3. The `make` command supports a new flag that introduces new testing/linting/building functionality AND/OR
4. A new CI workflow in GitHub or GitLab is created

The developer should also update [README.md](/README.md) with new options to the `make` command and other important commands that are essential for testing or building the tracer.

If these updates are not made, tell the developer to make changes or provide suggestions if requested.
