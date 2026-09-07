// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"bytes"
	"context"
	"os/exec"
	"regexp"
	"sort"
	"strings"
)

type Command struct {
	Path string
	Args []string
	Env  []string
	Dir  string
}

type CommandResult struct {
	Stdout string
	Stderr string
}

type CommandRunner interface {
	Run(context.Context, Command) (CommandResult, error)
}

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, command Command) (CommandResult, error) {
	cmd := exec.CommandContext(ctx, command.Path, command.Args...)
	cmd.Dir = command.Dir
	cmd.Env = append([]string(nil), command.Env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return CommandResult{Stdout: stdout.String(), Stderr: stderr.String()}, err
}

type GitClient struct {
	path   string
	runner CommandRunner
	env    []string
}

func NewGitClient(runner CommandRunner) GitClient {
	if runner == nil {
		runner = ExecRunner{}
	}
	return GitClient{path: "git", runner: runner, env: safeGitEnv()}
}

func safeGitEnv() []string {
	env := map[string]string{
		"GIT_CONFIG_GLOBAL":      "/dev/null",
		"GIT_CONFIG_NOSYSTEM":    "1",
		"GIT_CONFIG_SYSTEM":      "/dev/null",
		"GIT_NO_REPLACE_OBJECTS": "1",
		"GIT_OPTIONAL_LOCKS":     "0",
		"GIT_TERMINAL_PROMPT":    "0",
		"GOTOOLCHAIN":            "local",
		"GOWORK":                 "off",
		"LANG":                   "C.UTF-8",
		"LC_ALL":                 "C.UTF-8",
	}
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+env[key])
	}
	return result
}

func (g GitClient) ShowObject(ctx context.Context, dir, objectID string) (string, error) {
	if !ValidGitObjectID(objectID) {
		return "", typedContractError("invalid_git_object")
	}
	result, err := g.runner.Run(ctx, Command{
		Path: g.path,
		Args: []string{"-c", "protocol.file.allow=never", "-c", "core.hooksPath=/dev/null", "cat-file", "-t", objectID},
		Env:  append([]string(nil), g.env...),
		Dir:  dir,
	})
	if err != nil {
		return "", wrapReleaseError(ErrorClassEvidenceIncomplete, "git_read_failed", err)
	}
	return strings.TrimSpace(result.Stdout), nil
}

func ValidGitObjectID(objectID string) bool {
	return regexp.MustCompile(`^[a-f0-9]{40}$|^[a-f0-9]{64}$`).MatchString(objectID)
}
