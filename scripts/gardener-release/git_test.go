// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeRunner struct {
	commands []Command
	result   CommandResult
	err      error
}

func (r *fakeRunner) Run(_ context.Context, command Command) (CommandResult, error) {
	r.commands = append(r.commands, command)
	return r.result, r.err
}

func TestGitObjectIDRejectsAbbreviationsAndRevisionExpressions(t *testing.T) {
	invalid := []string{"abc123", "ABCDEFABCDEFABCDEFABCDEFABCDEFABCDEFABCD", "0123456789abcdef0123456789abcdef01234567^{commit}", "0123456789abcdef0123456789abcdef0123456g"}
	for _, value := range invalid {
		if ValidGitObjectID(value) {
			t.Fatalf("ValidGitObjectID(%q) = true", value)
		}
	}
	if !ValidGitObjectID("0123456789abcdef0123456789abcdef01234567") {
		t.Fatal("full lowercase SHA-1 object ID rejected")
	}
	if !ValidGitObjectID("0123456789abcdef0123456789abcdef012345670123456789abcdef01234567") {
		t.Fatal("full lowercase SHA-256 object ID rejected")
	}
}

func TestGitClientUsesArgumentArraysAndAllowlistedEnvironment(t *testing.T) {
	runner := &fakeRunner{result: CommandResult{Stdout: "commit\n"}}
	git := NewGitClient(runner)
	objectType, err := git.ShowObject(context.Background(), "/tmp/repo", "0123456789abcdef0123456789abcdef01234567")
	if err != nil {
		t.Fatal(err)
	}
	if objectType != "commit" {
		t.Fatalf("object type = %q, want commit", objectType)
	}
	if len(runner.commands) != 1 {
		t.Fatalf("commands = %d, want 1", len(runner.commands))
	}
	command := runner.commands[0]
	wantArgs := []string{"-c", "protocol.file.allow=never", "-c", "core.hooksPath=/dev/null", "cat-file", "-t", "0123456789abcdef0123456789abcdef01234567"}
	if strings.Join(command.Args, "\x00") != strings.Join(wantArgs, "\x00") {
		t.Fatalf("args = %#v, want %#v", command.Args, wantArgs)
	}
	joinedEnv := strings.Join(command.Env, "\n")
	for _, forbidden := range []string{"GIT_SSH_COMMAND", "BASH_ENV", "ENV="} {
		if strings.Contains(joinedEnv, forbidden) {
			t.Fatalf("environment contains forbidden key %q: %s", forbidden, joinedEnv)
		}
	}
	for _, required := range []string{"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_SYSTEM=/dev/null", "GIT_NO_REPLACE_OBJECTS=1", "GIT_TERMINAL_PROMPT=0", "GOWORK=off", "GOTOOLCHAIN=local"} {
		if !strings.Contains(joinedEnv, required) {
			t.Fatalf("environment missing %q: %s", required, joinedEnv)
		}
	}
}

func TestGitClientRejectsHostileObjectBeforeSubprocess(t *testing.T) {
	runner := &fakeRunner{}
	git := NewGitClient(runner)
	_, err := git.ShowObject(context.Background(), "/tmp/repo", "0123456789abcdef0123456789abcdef01234567;echo pwned")
	if ErrorCode(err) != "invalid_git_object" {
		t.Fatalf("error = %q, want invalid_git_object", ErrorCode(err))
	}
	if len(runner.commands) != 0 {
		t.Fatalf("runner called for invalid object: %#v", runner.commands)
	}
}

func TestGitReadFailureIsEvidenceIncomplete(t *testing.T) {
	runner := &fakeRunner{err: errors.New("fatal: not found")}
	git := NewGitClient(runner)
	_, err := git.ShowObject(context.Background(), "/tmp/repo", "0123456789abcdef0123456789abcdef01234567")
	if ErrorCode(err) != "git_read_failed" || ClassOf(err) != ErrorClassEvidenceIncomplete {
		t.Fatalf("error = %q/%q, want evidence_incomplete git_read_failed", ClassOf(err), ErrorCode(err))
	}
}
