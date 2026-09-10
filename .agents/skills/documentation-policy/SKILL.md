---
name: documentation-policy
description: Use when adding or reviewing significant features, make options or commands, CI workflows, or scoped AGENTS.md files.
user-invocable: false
metadata:
  internal: true
---

## Updating Documentation

This AGENTS.md should be short. Only update this file if a new AGENTS.md file is added, so it must be added to the list with its purpose.

Document changes in the nearest existing doc: the package's [Go doc comment](https://go.dev/doc/comment) or `doc.go` for API/behavior, the closest `README.md` (e.g. [`contrib/README.md`](contrib/README.md)) for an area, or [`FAQ.md`](FAQ.md)/[`MIGRATING.md`](MIGRATING.md) for user-facing notes. Reserve [`CONTRIBUTING.md`](CONTRIBUTING.md) for repo-wide contributor

1. It introduces a new method of interacting with and/or customizing the tracer (ie new scripts for generating files, options for configuration sources, etc)
2. A new internal functionality is introduced that can replace a common, built-in Go library
3. The `make` command supports a new flag that introduces new testing/linting/building functionality AND/OR
4. A new CI workflow in GitHub or GitLab is created

The developer should also update [README.md](./README.md) with new options to the `make` command and other important commands that are essential for testing or building the tracer.

If these updates are not made, tell the developer to make changes or provide suggestions if requested.
