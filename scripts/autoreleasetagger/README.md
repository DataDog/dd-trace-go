# Auto Release Tagger

This tool ensures that all modules in dd-trace-go are tagged correctly for release, respecting dependency order by tagging dependencies first before their dependents.

The repository has a structure consisting of nested modules for all the contribs and some packages to reduce the dependency surface to the strictly required dependencies.

- **Explicit Versioning**: Takes a `--version` flag; does not auto-determine versions from tags.
- **Nested Module Support**: Tags all modules in a repository.
- **Dependency Awareness**: Ensures dependencies are tagged before their dependents.

## 📌 Prerequisites

You must be on a `release-v<MAJOR>.<MINOR>.x` branch. The tool will refuse to run on `main`, feature branches, or a detached HEAD.

## 🛠️ Usage

Run the following command to tag the release:

```sh
go run ./scripts/autoreleasetagger --version v2.9.0-rc.1 --root ../..
```

Dry run: Simulate the tagging process to check for potential issues.

```sh
go run ./scripts/autoreleasetagger --version v2.9.0-rc.1 --dry-run --root ../..
```

Run without pushing tags: Tag all the modules to release but don't push them.

```sh
go run ./scripts/autoreleasetagger --version v2.9.0-rc.1 --disable-push --root ../..
```

## 📖 Help

[embedmd]:# (tmp/help.txt)
```txt
Usage of ./autoreleasetagger:
  -disable-push
    	Disable pushing tags to remote
  -dry-run
    	Enable dry run mode (skip actual operations)
  -exclude-dirs string
    	Comma-separated list of directories to exclude. Paths are relative to the root directory
  -exclude-modules string
    	Comma-separated list of modules to exclude
  -format string
    	Output format for errors: "text" or "json" (default "text")
  -loglevel string
    	Log level (debug, info, warn, error) (default "info")
  -remote string
    	Git remote name (default "origin")
  -root string
    	Path to the root directory (defaults to current directory) (default ".")
  -version string
    	Target release version (e.g. v2.9.0-rc.2)
```


## 🤖 CI Calling Convention

This section covers the exact CLI invocation, required environment,
exit-code semantics, and what to do for each error class.

### Invocation

```sh
autoreleasetagger \
  --version <v>          \  # e.g. v2.9.0-rc.2  (required)
  --root    <repo-root>  \  # absolute path to the dd-trace-go checkout
  --format  json         \  # machine-readable errors on stderr
  --disable-push            # tag pushes are NOT performed by this tool (see below)
```

The pipeline **always** passes `--format json` and `--disable-push`.  
Tag pushes are performed by the caller after verifying the local result (see
[Tag pushing](#tag-pushing)).

#### Required Git configuration

The tool creates a commit, so a committer identity must be present in the Git
config of the CI job before the tool is invoked:

```sh
git config user.email "release-bot@datadoghq.com"
git config user.name  "DD Release Bot"
```

These can be set globally (`--global`) in the CI image or locally in the
checkout.

#### Branch checkout requirement

The tool reads the current branch name to validate that the version's
`MAJOR.MINOR` matches the release branch. It will emit `invalid_branch` and
refuse to proceed in any of these states:

- **Detached HEAD** — GitLab CI pipelines triggered by a tag or a `git
  checkout <sha>` are in detached HEAD state. The job must explicitly check
  out the release branch *by name* before invoking the tool:
  ```sh
  git checkout release-v${MAJOR}.${MINOR}.x
  autoreleasetagger --version ${VERSION} ...
  ```
- **Wrong branch** — running on `main` or a feature branch is rejected.
- **Major/minor mismatch** — `--version v2.9.0-rc.1` on `release-v2.8.x` is
  rejected.

### Exit codes

| Exit code | Meaning |
|-----------|---------|
| `0` | Success — commit and tags created, **or** already at target version (no-op). |
| `1` | Failure — inspect the JSON object on stderr (see below). |

### JSON error format

When the tool exits `1` with `--format json`, it writes a single JSON object to
**stderr**:

```json
{
  "error":   "<code>",
  "message": "<human-readable description>",
  "details": { ... }
}
```

The `error` field uses a fixed vocabulary:

| Code | Meaning | Pipeline action |
|------|---------|-----------------|
| `dirty_tree` | The working tree has uncommitted changes or staged modifications. `details.modified_files` lists the affected paths. | Abort and alert. A clean checkout should never be dirty before the tool runs. |
| `tags_exist` | One or more target tags already exist — locally or on the remote — pointing at a **different** commit than HEAD. `details.conflicts` lists `{tag, commit, source}` triples where `source` is `"local"` or `"remote"`. | Alert the release manager. Manual investigation required before retrying (delete the conflicting tags or investigate the diverged commit). |
| `multi_commit_violation` | The tool produced more than one commit — invariant violated. This is a bug in the tool, not a recoverable state. | Abort immediately and file a bug. Do not retry until fixed. |
| `invalid_version` | The `--version` argument does not match `v<MAJOR>.<MINOR>.<PATCH>(-rc.<N>)?`. | Abort and alert. The pipeline should not have constructed a bad version string. |
| `invalid_branch` | The current branch is not a `release-v<MAJOR>.<MINOR>.x` branch, the checkout is in detached HEAD state, or the version's `MAJOR.MINOR` does not match the branch. | Abort and alert. Check out the correct release branch by name (`git checkout release-v<MAJOR>.<MINOR>.x`) before retrying. |
| `internal` | An unexpected internal error (wraps any error that does not carry a structured code). | Abort and alert. |

### Tag pushing

`autoreleasetagger` intentionally **does not push** when `--disable-push` is
set. After a successful run (`exit 0`) the pipeline must:

1. Verify the local state with `git tag --points-at HEAD` to confirm all
   expected tags were created.
2. Push tags to the remote:
   ```sh
   git push origin <root-tag> <contrib-tag-1> <contrib-tag-2> ...
   # or push all local tags that are not yet on the remote:
   git push origin --tags
   ```

This separation keeps `autoreleasetagger` runnable locally without push
credentials and makes the failure surface in CI cleaner.

### Idempotency

Running the tool twice with the same `--version` is a **no-op** (exit `0`, no
new commit, no duplicate tags) only when *all* expected tags already point at
HEAD and the version file matches. A partially-tagged state (e.g. an earlier
run was interrupted after the root tag was created but before all contrib tags
were written) is treated as incomplete: the tool will repair the missing tags
without adding a new commit.

The pipeline can safely retry on transient failures up to the point where all
tags exist on the correct commit.

## 🚀 Development

### Helpful Commands for Cleaning Up

```sh
export GIT_REMOTE=${GIT_REMOTE:-origin}

# List local-only tags
git tag -l | grep -v "$(git ls-remote --tags $GIT_REMOTE | sed 's/.*refs\/tags\///g')"

# Remove local-only tags
git tag -l | grep -v "$(git ls-remote --tags $GIT_REMOTE | sed 's/.*refs\/tags\///g')" | xargs git tag -d
```

#### Git Alias Setup

```sh
export GIT_REMOTE=${GIT_REMOTE:-origin}

# List tags that exist only locally
git config --global alias.list-local-tags "!git tag -l | grep -v \"$(git ls-remote --tags $GIT_REMOTE | sed 's/.*refs\/tags\///g')\""

# Remove tags that exist only locally
git config --global alias.remove-local-tags "!git tag -l | grep -v \"$(git ls-remote --tags $GIT_REMOTE | sed 's/.*refs\/tags\///g')\" | xargs git tag -d"
```

**Usage**

```sh
export GIT_REMOTE=${GIT_REMOTE:-origin}

# List local-only tags
git list-local-tags

# Remove local-only tags
git remove-local-tags
```
