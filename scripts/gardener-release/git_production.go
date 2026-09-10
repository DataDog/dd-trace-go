// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"context"
	"strings"
)

// ProtectedGitRunner adds the approved askpass environment only to Git
// commands that explicitly name the fixed production remote. All local Git
// plumbing retains its original allowlisted environment and never sees the
// publication token.
type ProtectedGitRunner struct {
	Runner      CommandRunner
	Credentials *GitHubGitCredentials
}

func (r ProtectedGitRunner) Run(ctx context.Context, command Command) (CommandResult, error) {
	if r.Runner == nil {
		return CommandResult{}, newReleaseError(ErrorClassEvidenceIncomplete, "runner_unavailable")
	}
	if command.Path != "git" || !containsCanonicalRemote(command.Args) {
		return r.Runner.Run(ctx, command)
	}
	if r.Credentials == nil {
		return CommandResult{}, newReleaseError(ErrorClassEvidenceIncomplete, "publication_token_unavailable")
	}
	credentialed, err := r.Credentials.Command(command.Dir, command.Args...)
	if err != nil {
		return CommandResult{}, err
	}
	result, runErr := r.Runner.Run(ctx, credentialed)
	if runErr != nil {
		// Never wrap a subprocess error with command environment or token.
		return result, wrapReleaseError(ErrorClassEvidenceIncomplete, "credentialed_git_failed", runErr)
	}
	return result, nil
}

func containsCanonicalRemote(args []string) bool {
	for _, arg := range args {
		if arg == CanonicalGitHubRemote {
			return true
		}
		if strings.Contains(arg, "github.com/DataDog/dd-trace-go") && arg != CanonicalGitHubRemote {
			return false
		}
	}
	return false
}
