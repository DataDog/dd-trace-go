# Auto Release Tagger

This tool ensures that all modules in a monorepo are tagged correctly, respecting dependency order—tagging dependencies first before their dependents.

- **Automatic Versioning**: Determines the next version based on existing tags.
- **Nested Module Support**: Tags all modules in a repository.
- **Dependency Awareness**: Ensures dependencies are tagged before their dependents.

## 📌 Prerequisites

Before running the tool, ensure you have updated the version according to the [Release Checklist](https://datadoghq.atlassian.net/wiki/spaces/DL/pages/2477949158/Template+v+MAJOR+.+MINOR+.+PATCH+Release+Checklist#Release-branch).

## 🛠️ Usage

Run the following command to tag the release:

```sh
go run ./tools/autoreleasetagger -root ../..
```

Dry run:

```sh
go run ./tools/autoreleasetagger -dry-run -root ../..
```

Run without pushing tags:

```sh
go run ./tools/autoreleasetagger -disable-push -root ../..
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
  -loglevel string
    	Log level (debug, info, warn, error) (default "info")
  -remote string
    	Git remote name (default "origin")
  -root string
    	Path to the root directory (required)
```


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
