// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

// Package main provides the autoreleasetagger command,
// which automates tagging and versioning of Go modules.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/DataDog/dd-trace-go/v2/internal/version"
)

var (
	defaultExcludedModules = []string{
		"github.com/DataDog/dd-trace-go/instrumentation/internal/namingschematest/v2",
	}
	defaultExcludedDirs = []string{
		"_tools",
		".github",
		"tools",
	}
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
		logLevel            string
		dryRun              bool
		excludeModulesInput string
		excludeDirsInput    string
		remote              string
		disablePush         bool
	)

	flag.StringVar(&root, "root", "", "Path to the root directory (required)")
	flag.StringVar(&logLevel, "loglevel", "info", "Log level (debug, info, warn, error)")
	flag.BoolVar(&dryRun, "dry-run", false, "Enable dry run mode (skip actual operations)")
	flag.StringVar(&excludeModulesInput, "exclude-modules", "", "Comma-separated list of modules to exclude")
	flag.StringVar(&excludeDirsInput, "exclude-dirs", "", "Comma-separated list of directories to exclude. Paths are relative to the root directory")
	flag.StringVar(&remote, "remote", "origin", "Git remote name")
	flag.BoolVar(&disablePush, "disable-push", false, "Disable pushing tags to remote")
	flag.Parse()

	if root == "" {
		fmt.Fprintln(os.Stderr, "ERROR: --root is required")
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

	version := version.Tag
	envVersion := os.Getenv("VERSION")
	if envVersion != "" {
		version = envVersion
	}

	// Pass disablePush to run.
	if err := run(dryRun, remote, disablePush, root, version, excludedModules, excludedDirs); err != nil {
		slog.Error("Failed to run autoreleasetagger", "error", err)
		os.Exit(1)
	}
}

// Updated run signature with disablePush.
func run(dryRun bool, remote string, disablePush bool, root, version string, excludedModules, excludedDirs []string) error {
	slog.Info("Using version", "version", version)

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

	// Tag and push the root module first.
	if err := tagAndPush(dryRun, disablePush, remote, rootModule, rootModule, version); err != nil {
		return fmt.Errorf("failed to tag root module: %w", err)
	}

	for _, modulePath := range sortedModules {
		mod := filteredModules[modulePath]
		slog.Info("Processing module", "module", mod.Module.Path)

		slog.Info("Updating dependencies", "module", mod.Module.Path)
		if err := updateDependencies(dryRun, rootModule, mod, version); err != nil {
			return fmt.Errorf("failed to update dependencies: %w", err)
		}

		slog.Info("Committing changes", "module", mod.Module.Path)
		if err := commitChangesIfNeeded(dryRun, rootModule, mod, version); err != nil {
			return fmt.Errorf("failed to commit changes: %w", err)
		}

		slog.Info("Tagging module", "module", mod.Module.Path)
		if err := tagAndPush(dryRun, disablePush, remote, rootModule, mod, version); err != nil {
			return fmt.Errorf("failed to tag module: %w", err)
		}
	}

	return nil
}

// Updated tagAndPush to accept disablePush.
func tagAndPush(dryRun, disablePush bool, remote string, root, mod GoMod, version string) error {
	tagName := version

	isRoot := root.Module.Path == mod.Module.Path
	if !isRoot {
		name, err := moduleShortName(root, mod)
		if err != nil {
			return fmt.Errorf("failed to get module short name: %w", err)
		}
		tagName = name + "/" + version
	}

	slog.Debug("Tagging module", "path", mod.Module.Path, "tag", tagName)
	if dryRun {
		slog.Debug("Skipping tagging/pushing in dry-run mode")
		return nil
	}

	if err := createTagIfMissing(mod.dir, tagName); err != nil {
		return err
	}

	if disablePush {
		slog.Debug("Pushing is disabled; skipping git push", "tag", tagName)
		return nil
	}

	return pushTagIfMissing(mod.dir, remote, tagName)
}

// createTagIfMissing checks for an existing local tag and creates it if needed.
func createTagIfMissing(dir, tagName string) error {
	existingTag, err := runCommandWithOutput(dir, "git", "tag", "--list", tagName)
	if err != nil {
		return fmt.Errorf("failed to check local tags for %s: %w", tagName, err)
	}
	if strings.TrimSpace(existingTag) != "" {
		slog.Info("Tag already exists locally; skipping creation", "tag", tagName)
		return nil
	}
	return runCommand(dir, "git", "tag", "-am", tagName, tagName)
}

// pushTagIfMissing checks if the remote tag exists and pushes only if it is missing.
func pushTagIfMissing(dir, remote, tagName string) error {
	remoteRef, err := runCommandWithOutput(dir, "git", "ls-remote", remote, "refs/tags/"+tagName)
	if err != nil {
		return fmt.Errorf("failed to check remote tags for %s: %w", tagName, err)
	}
	if strings.TrimSpace(remoteRef) != "" {
		slog.Info("Remote tag already exists; skipping push", "tag", tagName)
		return nil
	}
	slog.Debug("Pushing tag", "remote", remote, "tag", tagName)
	return runCommand(dir, "git", "push", remote, tagName)
}

// updateDependencies edits the given module dependencies from the root module.
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

// commitChangesIfNeeded checks if there are any changes, and if so, commits them.
func commitChangesIfNeeded(dryRun bool, root, mod GoMod, version string) error {
	changes, err := runCommandWithOutput(mod.dir, "git", "status", "--porcelain")
	if err != nil {
		return fmt.Errorf("failed to get git status: %w", err)
	}

	if len(strings.TrimSpace(changes)) == 0 {
		slog.Debug("No changes to commit", "module", mod.Module.Path)
		return nil
	}

	// Skip commit if go.mod is not changed.
	foundGoModChange := false
	for _, line := range strings.Split(changes, "\n") {
		if strings.Contains(line, "go.mod") {
			foundGoModChange = true
			break
		}
	}
	if !foundGoModChange {
		slog.Debug("No go.mod changes, skipping commit", "module", mod.Module.Path)
		return nil
	}

	name, err := moduleShortName(root, mod)
	if err != nil {
		return fmt.Errorf("failed to get module short name: %w", err)
	}
	msg := fmt.Sprintf("%s: %s", name, version)

	slog.Debug("Committing changes", "module", mod.Module.Path, "message", msg)
	if dryRun {
		slog.Debug("Skipping actual commit in dry-run mode")
		return nil
	}

	return runCommand(mod.dir, "git", "commit", "-am", msg)
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

// moduleShortName returns the short name of a module, relative to the root module.
func moduleShortName(root, mod GoMod) (string, error) {
	rel, err := filepath.Rel(root.dir, mod.dir)
	if err != nil {
		return "", fmt.Errorf("failed to get relative path: %w", err)
	}
	// e.g.
	// - github.com/DataDog/dd-trace-go/contrib/google.golang.org/api/v2 => contrib/google.golang.org/api/v2
	// - github.com/Datadog/dd-trace-go/contrib/envoyproxy/go-control-plane/v2 => contrib/envoyproxy/go-control-plane/v2
	return strings.TrimPrefix(rel, "../"), nil
}
