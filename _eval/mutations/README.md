# Task mutations

Patch files referenced by tasks whose mutation kind is `apply_patch`. One
directory per suite:

```
mutations/<suite-name>/<task-id>.patch
```

A mutation removes the work the agent is being asked to redo. Pick the kind by
what is being removed:

| Kind | Use |
| --- | --- |
| `delete_paths` | whole files or directories. No patch needed. |
| `apply_patch` | something smaller than a file, such as one registration entry in `instrumentation/packages.go`. |
| `none` | nothing: the task asks for something the repo does not have yet. Pair it with `assert_absent`. |

Patches live here rather than inside tasks because they are code: they need
reviewing, and they need to be diffable when they change.

Applied with `git apply`, so write the patch in the direction that performs the
removal.

Two rules the harness enforces:

- A patch must apply to **both** compared refs. If it applies to `main` but not to
  the branch head, the two sides are no longer running the same task, and the record
  is rejected rather than run.
- Patches go stale as `main` advances. `make verify` and the
  `TestMutationsApplyToHEAD` test both fail loudly when one no longer applies, which
  costs seconds instead of discovering it hours into a comparison.
