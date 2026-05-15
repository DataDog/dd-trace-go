// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

// Package main provides the autoreleasetagger command,
// which automates tagging and versioning of Go modules.
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// ---------------------------------------------------------------------------
// Structured errors
// ---------------------------------------------------------------------------

// errorCode is the machine-readable vocabulary used in --format json output.
// The pipeline pattern-matches on these values.
type errorCode string

const (
	errDirtyTree            errorCode = "dirty_tree"
	errTagsExist            errorCode = "tags_exist"
	errMultiCommitViolation errorCode = "multi_commit_violation"
	errInvalidVersion       errorCode = "invalid_version"
	errInvalidBranch        errorCode = "invalid_branch"
	errInternal             errorCode = "internal"
)

// StructuredError carries a fixed-vocabulary code together with a human
// message and an optional details bag that is included verbatim in JSON output.
type StructuredError struct {
	Code    errorCode
	Msg     string
	Details map[string]any
}

func (e *StructuredError) Error() string { return e.Msg }

// newStructuredError is a convenience constructor.
func newStructuredError(code errorCode, msg string, details map[string]any) *StructuredError {
	return &StructuredError{Code: code, Msg: msg, Details: details}
}

// renderError writes err to w in the requested format ("json" or prose).
// Pass os.Stderr from main; pass a *bytes.Buffer in tests to avoid racing on
// the process-global file descriptor.
func renderError(w io.Writer, err error, format string) {
	if err == nil {
		return
	}
	if format == "json" {
		var se *StructuredError
		if !errors.As(err, &se) {
			se = newStructuredError(errInternal, err.Error(), nil)
		}
		type jsonPayload struct {
			Error   errorCode      `json:"error"`
			Message string         `json:"message"`
			Details map[string]any `json:"details,omitempty"`
		}
		payload := jsonPayload{
			Error:   se.Code,
			Message: se.Msg,
			Details: se.Details,
		}
		b, _ := json.MarshalIndent(payload, "", "  ")
		fmt.Fprintln(w, string(b))
		return
	}
	// Human-readable prose.
	fmt.Fprintf(w, "ERROR: %s\n", err.Error())
}

var (
	// defaultUntaggedModules lists modules whose go.mod is updated and committed
	// as normal, but which are never tagged or pushed (e.g. internal test helpers
	// that carry no public API).
	defaultUntaggedModules = []string{
		"github.com/DataDog/dd-trace-go/instrumentation/internal/namingschematest/v2",
	}
	defaultExcludedModules = []string{}
	defaultExcludedDirs    = []string{
		"_tools",
		".github",
		"tools",
	}

	// versionFileRelPath is the path to the version file relative to the repo root.
	versionFileRelPath = filepath.Join("internal", "version", "version.go")

	// versionTagRe matches the var Tag line in version.go.
	versionTagRe = regexp.MustCompile(`^(var Tag = )".+"$`)

	// semverRe matches a valid release version: vMAJOR.MINOR.PATCH(-rc.N)?
	semverRe = regexp.MustCompile(`^v(\d+)\.(\d+)\.(\d+)(-rc\.\d+)?$`)

	// releaseBranchRe matches a release branch name: release-vMAJOR.MINOR.x
	releaseBranchRe = regexp.MustCompile(`^release-v(\d+)\.(\d+)\.x$`)
)

type (
	Module struct {
		Path    string
		Version string
	}

	GoMod struct {
		// dir is the directory where the module lives
		dir string

		Module    ModPath
		Go        string
		Toolchain string
		Require   []Require
		Exclude   []Module
		Replace   []Replace
		Retract   []Retract
	}

	ModPath struct {
		Path       string
		Deprecated string
	}

	Require struct {
		Path     string
		Version  string
		Indirect bool
	}

	Replace struct {
		Old Module
		New Module
	}

	Retract struct {
		Low       string
		High      string
		Rationale string
	}
)

func slogLevel(logLevel string) slog.Level {
	var lvl slog.Level

	switch logLevel {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}

	return lvl
}

func initLogger(logLevel string, dryRun bool) *slog.Logger {
	logLevelVar := slog.LevelVar{}
	if dryRun {
		logLevelVar.Set(slog.LevelDebug)
	} else {
		logLevelVar.Set(slogLevel(logLevel))
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: &logLevelVar}))
	slog.SetDefault(logger)

	return logger
}

func main() {
	var (
		root                string
		version             string
		logLevel            string
		format              string
		dryRun              bool
		excludeModulesInput string
		untagModulesInput   string
		excludeDirsInput    string
		remote              string
		disablePush         bool
	)

	flag.StringVar(&root, "root", ".", "Path to the root directory (defaults to current directory)")
	flag.StringVar(&version, "version", "", "Target release version (e.g. v2.9.0-rc.2)")
	flag.StringVar(&logLevel, "loglevel", "info", "Log level (debug, info, warn, error)")
	flag.StringVar(&format, "format", "text", `Output format for errors: "text" (default) or "json"`)
	flag.BoolVar(&dryRun, "dry-run", false, "Enable dry run mode (skip actual operations)")
	flag.StringVar(&excludeModulesInput, "exclude-modules", "", "Comma-separated list of modules to exclude entirely (no go.mod update, no tag)")
	flag.StringVar(&untagModulesInput, "untag-modules", "", "Comma-separated list of modules to update go.mod for but not tag or push")
	flag.StringVar(&excludeDirsInput, "exclude-dirs", "", "Comma-separated list of directories to exclude. Paths are relative to the root directory")
	flag.StringVar(&remote, "remote", "origin", "Git remote name")
	flag.BoolVar(&disablePush, "disable-push", false, "Disable pushing tags to remote")
	flag.Parse()

	if version == "" {
		fmt.Fprintln(os.Stderr, "ERROR: --version is required")
		os.Exit(1)
	}

	_ = initLogger(logLevel, dryRun)

	slog.Info("Starting autoreleasetagger")
	if dryRun {
		slog.Debug("Running in dry-run mode, skipping actual operations")
	}

	root, err := filepath.Abs(root)
	if err != nil {
		slog.Error("Failed to convert root path to absolute", "error", err)
		os.Exit(1)
	}
	slog.Info("Using root directory", "root", root)

	excludedModules := append([]string{}, defaultExcludedModules...)
	if excludeModulesInput != "" {
		modules := strings.Split(excludeModulesInput, ",")
		for _, m := range modules {
			excludedModules = append(excludedModules, strings.TrimSpace(m))
		}
	}

	untaggedModules := append([]string{}, defaultUntaggedModules...)
	if untagModulesInput != "" {
		modules := strings.Split(untagModulesInput, ",")
		for _, m := range modules {
			untaggedModules = append(untaggedModules, strings.TrimSpace(m))
		}
	}

	// These paths are relative to the root directory.
	excludedDirs := append([]string{}, defaultExcludedDirs...)
	for i, d := range excludedDirs {
		excludedDirs[i] = filepath.Join(root, d)
	}

	if excludeDirsInput != "" {
		dirs := strings.Split(excludeDirsInput, ",")
		for _, d := range dirs {
			excludedDirs = append(excludedDirs, filepath.Join(root, strings.TrimSpace(d)))
		}
	}

	if err := run(dryRun, remote, disablePush, root, version, excludedModules, untaggedModules, excludedDirs); err != nil {
		renderError(os.Stderr, err, format)
		os.Exit(1)
	}
}

func run(dryRun bool, remote string, disablePush bool, root, version string, excludedModules, untaggedModules, excludedDirs []string) error {
	slog.Info("Using version", "version", version)

	// Validate version format and branch consistency.
	if err := validateVersionAndBranch(root, version); err != nil {
		return err
	}

	modules, err := findModules(root, excludedDirs)
	if err != nil {
		return fmt.Errorf("failed to find modules: %w", err)
	}
	slog.Info("Found modules:", "count", len(modules))
	moduleKeys := make([]string, 0, len(modules))
	for k := range modules {
		moduleKeys = append(moduleKeys, k)
	}
	slog.Debug("Found modules", "modules", moduleKeys)

	// Assumption: There is a go module in the root directory.
	rootModule, err := readModule(filepath.Join(root, "go.mod"))
	if err != nil {
		return fmt.Errorf("failed to read root module: %w", err)
	}
	slog.Info("Root module", "module", rootModule.Module.Path)

	// Filter modules to only include those that depend on the root module.
	filteredModules := filterModules(modules, rootModule.Module.Path, excludedModules)
	slog.Info("Filtered modules:", "count", len(filteredModules))
	filteredKeys := make([]string, 0, len(filteredModules))
	for k := range filteredModules {
		filteredKeys = append(filteredKeys, k)
	}
	slog.Debug("Filtered modules", "modules", filteredKeys)

	sortedModules, err := topologicalSort(buildDependencyGraph(filteredModules))
	if err != nil {
		return fmt.Errorf("failed to topologically sort modules: %w", err)
	}

	// Build the complete expected tag list now (root + every contrib) so we can
	// use it for both the idempotency check and the pre-mutation guard below.
	// allTags is also used in Phase 3; computing it once avoids a second walk.
	allTags := buildTagList(root, rootModule, filteredModules, sortedModules, version, untaggedModules)

	// Idempotency check: if the version file already matches AND every expected
	// tag already points at HEAD, the tool has fully run — treat as a no-op.
	// Checking all tags (not just the root) prevents silently leaving a partial
	// tag set when a previous run was interrupted after the root tag was created
	// but before all contrib tags were written.
	if alreadyDone, err := isAlreadyTagged(root, version, allTags); err != nil {
		return fmt.Errorf("idempotency check failed: %w", err)
	} else if alreadyDone {
		slog.Warn("Already at target version, nothing to do", "version", version)
		return nil
	}

	// Safety guard 1: refuse to run on a dirty working tree.
	if err := checkDirtyTree(root); err != nil {
		return err
	}

	// Safety guard 2: refuse if any of the target tags already exist on a
	// different commit (locally or on the remote). The idempotency path above
	// handles the "all tags already point at HEAD" case; this guard catches
	// tags that point somewhere else — including tags that were pushed from a
	// different run and are only visible via the remote.
	if err := checkTagsExist(root, remote, allWouldBeTags(root, version, excludedDirs, untaggedModules)); err != nil {
		return err
	}

	// Record HEAD before any mutation so we can assert the single-commit
	// invariant afterwards.
	preHead, err := runCommandWithOutput(root, "git", "rev-parse", "HEAD")
	if err != nil {
		return fmt.Errorf("failed to read HEAD: %w", err)
	}
	preHead = strings.TrimSpace(preHead)

	// Phase 1: stage all file mutations without committing.
	// Skip if the version was already committed (repair path: the release commit
	// exists but one or more tags are missing). Re-running go mod edit/tidy
	// when the files are already correct risks a spurious second commit if the
	// tools rewrite go.mod with cosmetically different content.
	slog.Info("Phase 1: staging all mutations")

	if versionAlreadyCommitted(root, version) {
		slog.Info("Version already committed; skipping file mutations (repair path)")
	} else {
		if err := updateVersionFile(dryRun, root, version); err != nil {
			return fmt.Errorf("failed to update version file: %w", err)
		}

		for _, modulePath := range sortedModules {
			mod := filteredModules[modulePath]
			slog.Info("Updating dependencies", "module", mod.Module.Path)
			if err := updateDependencies(dryRun, rootModule, mod, version); err != nil {
				return fmt.Errorf("failed to update dependencies for %s: %w", mod.Module.Path, err)
			}
		}
	}

	// Phase 2: one single commit for all mutations.
	slog.Info("Phase 2: committing all changes")
	if err := commitAll(dryRun, root, version); err != nil {
		return fmt.Errorf("failed to commit: %w", err)
	}

	// Safety guard 3: paranoia check — assert that exactly one commit was
	// added. If this ever fires it means a future refactor reintroduced
	// multi-commit behaviour; fail loudly so it is caught immediately.
	if !dryRun {
		if err := assertSingleCommit(root, preHead); err != nil {
			return err
		}
	}

	// Phase 3: create all tags pointing at the single commit.
	slog.Info("Phase 3: tagging all modules")
	for _, tagName := range allTags {
		slog.Info("Tagging", "tag", tagName)
		if err := createTagIfMissing(dryRun, root, tagName); err != nil {
			return fmt.Errorf("failed to create tag %s: %w", tagName, err)
		}
	}

	if disablePush {
		slog.Info("Push disabled; skipping git push")
		return nil
	}

	for i := 0; i < len(allTags); i += 3 {
		batch := allTags[i:min(i+3, len(allTags))]
		if err := pushTagsBatch(dryRun, root, remote, batch); err != nil {
			return fmt.Errorf("failed to push tags %v: %w", batch, err)
		}
	}

	return nil
}

// validateVersionAndBranch ensures the version matches the expected pattern and is
// consistent with the current release branch (release-vMAJOR.MINOR.x).
func validateVersionAndBranch(root, version string) error {
	vm := semverRe.FindStringSubmatch(version)
	if vm == nil {
		return newStructuredError(
			errInvalidVersion,
			fmt.Sprintf("invalid version %q: must match v<MAJOR>.<MINOR>.<PATCH>(-rc.<N>)?", version),
			map[string]any{"version": version},
		)
	}
	verMajor, verMinor := vm[1], vm[2]

	branch, err := currentBranch(root)
	if err != nil {
		return fmt.Errorf("failed to determine current branch: %w", err)
	}

	bm := releaseBranchRe.FindStringSubmatch(branch)
	if bm == nil {
		return newStructuredError(
			errInvalidBranch,
			fmt.Sprintf("current branch %q is not a release branch (expected release-v<MAJOR>.<MINOR>.x)", branch),
			map[string]any{"branch": branch},
		)
	}
	branchMajor, branchMinor := bm[1], bm[2]

	if verMajor != branchMajor || verMinor != branchMinor {
		return newStructuredError(
			errInvalidBranch,
			fmt.Sprintf("version %s does not match branch %s (major.minor mismatch: %s.%s vs %s.%s)",
				version, branch, verMajor, verMinor, branchMajor, branchMinor),
			map[string]any{"version": version, "branch": branch},
		)
	}
	return nil
}

// currentBranch returns the name of the current git branch.
// It returns an invalid_branch StructuredError when the checkout is in
// detached HEAD state (rev-parse --abbrev-ref returns "HEAD"), because CI
// jobs that use a shallow or detached clone must explicitly check out the
// release branch by name before invoking this tool.
func currentBranch(dir string) (string, error) {
	out, err := runCommandWithOutput(dir, "git", "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", err
	}
	branch := strings.TrimSpace(out)
	if branch == "HEAD" {
		return "", newStructuredError(
			errInvalidBranch,
			"repository is in detached HEAD state: check out the release branch by name before running autoreleasetagger",
			map[string]any{"hint": "git checkout release-v<MAJOR>.<MINOR>.x"},
		)
	}
	return branch, nil
}

// checkDirtyTree inspects the working tree with `git status --porcelain` and
// returns a dirty_tree StructuredError if any tracked file has uncommitted
// changes or staged modifications. Untracked files that git does not know
// about are ignored to avoid false-positives from editor temp files.
func checkDirtyTree(root string) error {
	out, err := runCommandWithOutput(root, "git", "status", "--porcelain")
	if err != nil {
		return fmt.Errorf("git status failed: %w", err)
	}
	var dirty []string
	for _, line := range strings.Split(out, "\n") {
		if len(line) < 2 {
			continue
		}
		xy := line[:2]
		filePath := strings.TrimSpace(line[2:])
		// '??' = untracked; '!!' = ignored — skip both.
		if xy == "??" || xy == "!!" {
			continue
		}
		if filePath != "" {
			dirty = append(dirty, filePath)
		}
	}
	if len(dirty) == 0 {
		return nil
	}
	return newStructuredError(
		errDirtyTree,
		fmt.Sprintf("working tree has uncommitted changes in: %s", strings.Join(dirty, ", ")),
		map[string]any{"modified_files": dirty},
	)
}

// allWouldBeTags returns a conservative approximation of the tag list derived
// solely from the filesystem (before modules are fully resolved). It is used
// only by the pre-mutation tags_exist guard; the real tag list is built later
// via buildTagList after modules are discovered.
//
// excludedDirs must be the same resolved slice passed to findModules so that
// modules in _tools, .github, tools, etc. are not included — those directories
// are never tagged and their presence in the list would cause false-positive
// tags_exist errors if they happened to carry an old tag on a different commit.
func allWouldBeTags(root, version string, excludedDirs, untaggedModules []string) []string {
	// Walk the repo and collect every go.mod that is NOT the root so we can
	// build the tag list without running the full module-discovery pipeline.
	tags := []string{version}
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			if containsPath(excludedDirs, path) {
				return filepath.SkipDir
			}
		}
		if d.Name() != "go.mod" || filepath.Dir(path) == root {
			return nil
		}
		rel, err := filepath.Rel(root, filepath.Dir(path))
		if err != nil {
			return nil
		}
		// Read the module path to check against untaggedModules.
		mod, err := readModule(path)
		if err == nil && containsPath(untaggedModules, mod.Module.Path) {
			return nil
		}
		tags = append(tags, rel+"/"+version)
		return nil
	})
	return tags
}

// checkTagsExist inspects the provided tag names and returns a tags_exist
// StructuredError if any of them already exist — locally or on the remote —
// pointing at a commit other than HEAD. Tags that point at HEAD are handled
// by the idempotency check and are not flagged here.
//
// Checking the remote is important in CI: a fresh clone will not have fetched
// tags, so a tag that was pushed by a previous (diverged) run would be
// invisible to a local-only check.
func checkTagsExist(root, remote string, tags []string) error {
	head, err := runCommandWithOutput(root, "git", "rev-parse", "HEAD")
	if err != nil {
		return fmt.Errorf("failed to read HEAD: %w", err)
	}
	head = strings.TrimSpace(head)

	type conflict struct {
		Tag    string `json:"tag"`
		Commit string `json:"commit"`
		Source string `json:"source"` // "local" or "remote"
	}
	var conflicts []conflict
	seen := make(map[string]bool) // deduplicate across local + remote

	// --- local tags ---
	for _, tag := range tags {
		out, err := runCommandWithOutput(root, "git", "tag", "--list", tag)
		if err != nil || strings.TrimSpace(out) == "" {
			continue // tag does not exist locally
		}
		// Resolve the tag to the commit it points at (dereferences annotated tags).
		commit, err := runCommandWithOutput(root, "git", "rev-list", "-n1", tag)
		if err != nil {
			continue
		}
		commit = strings.TrimSpace(commit)
		if commit != head {
			conflicts = append(conflicts, conflict{Tag: tag, Commit: commit, Source: "local"})
			seen[tag] = true
		}
	}

	// --- remote tags ---
	// Query the remote for all matching tags in one ls-remote call. Skip if the
	// remote is empty (e.g. in tests that pass "test-remote" with no actual remote).
	if remote != "" {
		remoteRefs, err := runCommandWithOutput(root, "git", "ls-remote", "--tags", remote)
		if err == nil && strings.TrimSpace(remoteRefs) != "" {
			// Build a map of refname -> commit from the ls-remote output.
			// Each line is: "<commit>\trefs/tags/<tag>" or "<commit>\trefs/tags/<tag>^{}"
			// The "^{}" suffix is the peeled (dereferenced) commit for annotated tags;
			// we prefer the peeled entry when present.
			peeledCommit := make(map[string]string) // tag -> commit (peeled preferred)
			for _, line := range strings.Split(remoteRefs, "\n") {
				parts := strings.SplitN(line, "\t", 2)
				if len(parts) != 2 {
					continue
				}
				commit, ref := parts[0], parts[1]
				const prefix = "refs/tags/"
				if !strings.HasPrefix(ref, prefix) {
					continue
				}
				tagName := strings.TrimPrefix(ref, prefix)
				peeled := strings.HasSuffix(tagName, "^{}")
				if peeled {
					tagName = strings.TrimSuffix(tagName, "^{}")
				}
				// Prefer the peeled (dereferenced) commit; only set if not already set.
				if peeled || peeledCommit[tagName] == "" {
					peeledCommit[tagName] = commit
				}
			}
			for _, tag := range tags {
				if seen[tag] {
					continue // already reported from local
				}
				commit, exists := peeledCommit[tag]
				if !exists {
					continue // not on remote
				}
				if commit != head {
					conflicts = append(conflicts, conflict{Tag: tag, Commit: commit, Source: "remote"})
				}
			}
		}
		// If ls-remote fails (e.g. no network, test remote), skip the remote
		// check silently — the local check still applies.
	}

	if len(conflicts) == 0 {
		return nil
	}

	names := make([]string, len(conflicts))
	for i, c := range conflicts {
		names[i] = fmt.Sprintf("%s@%s(%s)", c.Tag, c.Commit[:min(7, len(c.Commit))], c.Source)
	}
	return newStructuredError(
		errTagsExist,
		fmt.Sprintf("tags already exist on a different commit: %s", strings.Join(names, ", ")),
		map[string]any{"conflicts": conflicts},
	)
}

// assertSingleCommit verifies that exactly one commit was added between
// preHead and the current HEAD. It returns a multi_commit_violation
// StructuredError if the count is anything other than 1, or 0.
// count == "0" is valid in the repair path: when a previous run already
// committed the version bump but left some tags missing, Phase 1 is skipped
// and commitAll produces no new commit — Phase 3 then creates the missing tags.
func assertSingleCommit(root, preHead string) error {
	// Count commits reachable from HEAD but not from preHead.
	out, err := runCommandWithOutput(root, "git", "rev-list", "--count", preHead+"..HEAD")
	if err != nil {
		return fmt.Errorf("rev-list failed: %w", err)
	}
	count := strings.TrimSpace(out)
	if count == "0" || count == "1" {
		return nil
	}
	currentHead, _ := runCommandWithOutput(root, "git", "rev-parse", "HEAD")
	return newStructuredError(
		errMultiCommitViolation,
		fmt.Sprintf("invariant violated: expected 0 or 1 new commit, got %s", count),
		map[string]any{"pre_head": preHead, "current_head": strings.TrimSpace(currentHead), "new_commit_count": count},
	)
}

// versionAlreadyCommitted reports whether the version file already records
// the target version AND the most recent commit message matches the expected
// release commit format. Both conditions together mean the release commit was
// already produced by a previous (possibly interrupted) run; only tags may be
// missing, so Phase 1 (file mutations) must be skipped to avoid a spurious
// second commit.
func versionAlreadyCommitted(root, version string) bool {
	current, err := readVersionFile(root)
	if err != nil || current != version {
		return false
	}
	// Verify that the HEAD commit carries the expected release message.
	expectedMsg := fmt.Sprintf("internal/version: %s", version)
	out, err := runCommandWithOutput(root, "git", "log", "-1", "--format=%s")
	if err != nil {
		return false
	}
	return strings.TrimSpace(out) == expectedMsg
}

// ---------------------------------------------------------------------------

// isAlreadyTagged returns true when the version file already contains the target
// version AND every expected tag in wantTags already points at HEAD.
//
// Checking all tags (not just the root) prevents treating a partially-completed
// run as done: if a previous invocation was interrupted after creating the root
// tag but before all contrib tags were written, the next run must continue
// rather than silently exit as a no-op.
func isAlreadyTagged(root, version string, wantTags []string) (bool, error) {
	current, err := readVersionFile(root)
	if err != nil {
		// If the file doesn't exist yet, we are definitely not done.
		return false, nil
	}
	if current != version {
		return false, nil
	}

	// Collect the set of tags that currently point at HEAD.
	tagsAtHead, err := runCommandWithOutput(root, "git", "tag", "--points-at", "HEAD")
	if err != nil {
		return false, err
	}
	atHead := make(map[string]bool)
	for _, t := range strings.Split(tagsAtHead, "\n") {
		if s := strings.TrimSpace(t); s != "" {
			atHead[s] = true
		}
	}

	// Every expected tag must be present at HEAD.
	for _, tag := range wantTags {
		if !atHead[tag] {
			return false, nil
		}
	}
	return true, nil
}

// updateVersionFile rewrites the `var Tag = "..."` line in internal/version/version.go.
func updateVersionFile(dryRun bool, root, version string) error {
	path := filepath.Join(root, versionFileRelPath)
	slog.Debug("Updating version file", "path", path, "version", version)

	if dryRun {
		slog.Debug("Skipping version file update in dry-run mode")
		return nil
	}

	in, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("failed to open %s: %w", path, err)
	}
	defer in.Close()

	var lines []string
	updated := false
	scanner := bufio.NewScanner(in)
	for scanner.Scan() {
		line := scanner.Text()
		if versionTagRe.MatchString(line) {
			line = versionTagRe.ReplaceAllString(line, `${1}"`+version+`"`)
			updated = true
		}
		lines = append(lines, line)
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("failed to scan %s: %w", path, err)
	}
	if !updated {
		return fmt.Errorf("var Tag line not found in %s", path)
	}

	content := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("failed to write %s: %w", path, err)
	}
	return nil
}

// readVersionFile returns the version string currently recorded in version.go.
func readVersionFile(root string) (string, error) {
	path := filepath.Join(root, versionFileRelPath)
	in, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer in.Close()

	re := regexp.MustCompile(`^var Tag = "(.+)"$`)
	scanner := bufio.NewScanner(in)
	for scanner.Scan() {
		if m := re.FindStringSubmatch(scanner.Text()); m != nil {
			return m[1], nil
		}
	}
	return "", fmt.Errorf("var Tag line not found in %s", path)
}

// commitAll stages every change in the working tree from the repo root and
// produces exactly one commit.
func commitAll(dryRun bool, root, version string) error {
	msg := fmt.Sprintf("internal/version: %s", version)
	slog.Debug("Committing all changes", "message", msg)
	if dryRun {
		slog.Debug("Skipping commit in dry-run mode")
		return nil
	}

	// Check if there is anything to commit, counting only tracked changes.
	// Untracked files ("??") and ignored files ("+!!") are skipped — the same
	// logic as checkDirtyTree — so that stray editor/build artefacts present
	// during a repair run (version already committed, some tags missing) do not
	// cause a spurious "nothing to commit" decision or a failed git commit.
	status, err := runCommandWithOutput(root, "git", "status", "--porcelain")
	if err != nil {
		return fmt.Errorf("git status failed: %w", err)
	}
	hasTrackedChanges := false
	for _, line := range strings.Split(status, "\n") {
		if len(line) < 2 {
			continue
		}
		xy := line[:2]
		if xy == "??" || xy == "!!" {
			continue
		}
		hasTrackedChanges = true
		break
	}
	if !hasTrackedChanges {
		slog.Debug("Nothing to commit")
		return nil
	}

	// Stage only modifications to already-tracked files (and deletions).
	// Using --update instead of --all ensures that untracked files (editor
	// swap files, stray build artefacts) are never swept into the release
	// commit. The dirty-tree guard already rejected any tracked dirt before
	// mutations ran, so only the files the tool itself wrote are staged here.
	if err := runCommand(root, "git", "add", "--update"); err != nil {
		return fmt.Errorf("git add failed: %w", err)
	}
	return runCommand(root, "git", "commit", "-m", msg)
}

// buildTagList constructs the ordered list of tag names for root + all contrib modules.
func buildTagList(root string, rootModule GoMod, filteredModules map[string]GoMod, sortedModules []string, version string, untaggedModules []string) []string {
	tags := []string{version}
	for _, modulePath := range sortedModules {
		mod := filteredModules[modulePath]
		if containsPath(untaggedModules, mod.Module.Path) {
			slog.Info("Skipping tag for untagged module", "module", mod.Module.Path)
			continue
		}
		name, err := moduleShortName(rootModule, mod)
		if err != nil {
			slog.Warn("Could not compute short name for module; skipping tag", "module", mod.Module.Path, "error", err)
			continue
		}
		tags = append(tags, name+"/"+version)
	}
	return tags
}

// createTagIfMissing creates a tag at HEAD if it doesn't already exist locally.
func createTagIfMissing(dryRun bool, dir, tagName string) error {
	existingTag, err := runCommandWithOutput(dir, "git", "tag", "--list", tagName)
	if err != nil {
		return fmt.Errorf("failed to check local tags for %s: %w", tagName, err)
	}
	if strings.TrimSpace(existingTag) != "" {
		slog.Info("Tag already exists locally; skipping creation", "tag", tagName)
		return nil
	}
	if dryRun {
		slog.Debug("Skipping tag creation in dry-run mode", "tag", tagName)
		return nil
	}
	return runCommand(dir, "git", "tag", "-am", tagName, tagName)
}

// pushTagsBatch pushes up to 3 tags in a single git push invocation, skipping
// any that already exist on the remote.
func pushTagsBatch(dryRun bool, dir, remote string, tagNames []string) error {
	var toPush []string
	for _, tagName := range tagNames {
		remoteRef, err := runCommandWithOutput(dir, "git", "ls-remote", remote, "refs/tags/"+tagName)
		if err != nil {
			return fmt.Errorf("failed to check remote tag %s: %w", tagName, err)
		}
		if strings.TrimSpace(remoteRef) != "" {
			slog.Info("Remote tag already exists; skipping push", "tag", tagName)
			continue
		}
		toPush = append(toPush, tagName)
	}
	if len(toPush) == 0 {
		return nil
	}
	slog.Debug("Pushing tags", "remote", remote, "tags", toPush)
	if dryRun {
		slog.Debug("Skipping push in dry-run mode", "tags", toPush)
		return nil
	}
	// Use explicit refspecs (refs/tags/X:refs/tags/X) and --no-atomic so that
	// git does not advertise or bundle unrelated local refs into this push.
	// GitHub enforces a rule of at most 5 refs updated per push; without these
	// flags git's send-pack negotiates all locally-ahead tags in one session,
	// causing GitHub to count and reject the entire batch.
	args := []string{"push", "--no-atomic", remote}
	for _, tag := range toPush {
		args = append(args, "refs/tags/"+tag+":refs/tags/"+tag)
	}
	return runCommand(dir, "git", args...)
}

// updateDependencies edits the given module's dependencies on the root module.
func updateDependencies(dryRun bool, rootModule, mod GoMod, version string) error {
	slog.Debug("Edit dd-trace-go dependencies", "module", mod.Module.Path)
	if dryRun {
		slog.Debug("Skipping editing dd-trace-go dependencies in dry-run mode")
		return nil
	}

	prefix := strings.Replace(rootModule.Module.Path, "/v2", "", 1)
	for _, req := range mod.Require {
		// Exclude dependencies that are not part of dd-trace-go.
		if !strings.HasPrefix(req.Path, prefix) {
			continue
		}
		require := fmt.Sprintf("-require=%s@%s", req.Path, version)
		if err := runCommand(mod.dir, "go", "mod", "edit", require); err != nil {
			return err
		}
	}

	if err := syncDependencies(dryRun, mod); err != nil {
		return fmt.Errorf("failed to sync dependencies: %w", err)
	}
	return nil
}

// syncDependencies runs 'go mod tidy' on the given module.
func syncDependencies(dryRun bool, mod GoMod) error {
	slog.Debug("Running go mod tidy", "module", mod.Module.Path)
	if dryRun {
		slog.Debug("Skipping go mod tidy in dry-run mode")
		return nil
	}

	return runCommand(mod.dir, "go", "mod", "tidy")
}

// runCommandWithOutput runs a command in the specified directory and returns any output.
func runCommandWithOutput(dir string, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to run command %q: %w", cmd.String(), err)
	}
	return string(output), nil
}

// runCommand runs a command in the specified directory.
func runCommand(dir string, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to run command %q: %w", cmd.String(), err)
	}
	return nil
}

// filterModules returns only modules that depend on the root module.
func filterModules(modules map[string]GoMod, rootModulePath string, excludedModules []string) map[string]GoMod {
	filtered := make(map[string]GoMod)

	for path, mod := range modules {
		// Skip if directory name or module path is excluded.
		if containsPath(excludedModules, path) {
			continue
		}
		// Include only if it depends on the root module.
		for _, req := range mod.Require {
			if req.Path == rootModulePath {
				filtered[path] = mod
				break
			}
		}
	}

	return filtered
}

func findModules(root string, excludedDirs []string) (map[string]GoMod, error) {
	modules := make(map[string]GoMod)

	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if entry.IsDir() && (containsPath(excludedDirs, path) || entry.Name() == ".git") {
			slog.Debug("Skipping directory", "path", path)
			return filepath.SkipDir
		}

		if entry.Name() == "go.mod" {
			m, err := readModule(path)
			if err != nil {
				return fmt.Errorf("failed to read module: %w", err)
			}

			modules[m.Module.Path] = m
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to walk directory: %w", err)
	}
	return modules, nil
}

func readModule(path string) (GoMod, error) {
	cmd := exec.Command("go", "mod", "edit", "-json", path)
	cmd.Stderr = os.Stderr

	output, err := cmd.Output()
	if err != nil {
		return GoMod{}, fmt.Errorf("failed to run go mod edit: %w", err)
	}

	m := GoMod{dir: filepath.Dir(path)}
	if err := json.Unmarshal(output, &m); err != nil {
		return GoMod{}, fmt.Errorf("failed to unmarshal go.mod: %w", err)
	}

	return m, nil
}

// buildDependencyGraph parses each GoMod's Require field to link module paths that depend on each other.
func buildDependencyGraph(modules map[string]GoMod) map[string][]string {
	graph := make(map[string][]string)
	// Ensure every module is a key, even if it has no edges.
	for path := range modules {
		graph[path] = []string{}
	}
	// Invert edges: if module A depends on B, store B -> A.
	for path, mod := range modules {
		for _, req := range mod.Require {
			if _, ok := modules[req.Path]; ok {
				graph[req.Path] = append(graph[req.Path], path)
			}
		}
	}

	return graph
}

// topologicalSort orders modules so dependencies come before dependents.
func topologicalSort(graph map[string][]string) ([]string, error) {
	inDegree := make(map[string]int)
	for node := range graph {
		inDegree[node] = 0
	}

	for _, adj := range graph {
		for _, dep := range adj {
			inDegree[dep]++
		}
	}

	var queue []string

	for node, deg := range inDegree {
		if deg == 0 {
			queue = append(queue, node)
		}
	}

	var sorted []string

	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]

		sorted = append(sorted, curr)

		for _, dep := range graph[curr] {
			inDegree[dep]--
			if inDegree[dep] == 0 {
				queue = append(queue, dep)
			}
		}
	}

	if len(sorted) != len(graph) {
		return nil, errors.New("cycle detected in module dependencies")
	}

	return sorted, nil
}

// containsPath checks if a string appears in a slice.
func containsPath(list []string, s string) bool {
	for _, item := range list {
		eql, err := normalizedPathEqual(item, s)
		if err != nil {
			slog.Error("Failed to compare paths", "error", err)
			continue
		}

		if eql {
			return true
		}
	}

	return false
}

// normalizedPathEqual checks if two paths are equal after normalizing and converting to absolute paths.
func normalizedPathEqual(path1, path2 string) (bool, error) {
	abs1, err := filepath.Abs(path1)
	if err != nil {
		return false, fmt.Errorf("failed to convert %q to absolute: %w", path1, err)
	}

	abs2, err := filepath.Abs(path2)
	if err != nil {
		return false, fmt.Errorf("failed to convert %q to absolute: %w", path2, err)
	}

	return filepath.Clean(abs1) == filepath.Clean(abs2), nil
}

// moduleShortName returns the filesystem path of mod's directory relative to
// the root module's directory. This is used as the tag prefix — Go's multi-
// module tagging convention uses the directory path, not the module import
// path. For dd-trace-go, where contrib modules keep go.mod at the module
// directory root (no /vN subdirectory), the two coincide:
//
//	dir: contrib/google.golang.org/api  →  tag prefix: contrib/google.golang.org/api
//	dir: contrib/envoyproxy/go-control-plane  →  tag prefix: contrib/envoyproxy/go-control-plane
func moduleShortName(root, mod GoMod) (string, error) {
	rel, err := filepath.Rel(root.dir, mod.dir)
	if err != nil {
		return "", fmt.Errorf("failed to get relative path: %w", err)
	}
	return strings.TrimPrefix(rel, "../"), nil
}
