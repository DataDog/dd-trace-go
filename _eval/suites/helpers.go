// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package suites

import "github.com/DataDog/dd-trace-go/_eval/internal/agenteval"

// Building blocks shared by every suite. They exist so a task reads as a list of
// what it asks for, and so that two suites checking the same thing end up with
// the same criterion name and therefore the same column.

// CommonForbidden are paths no task should need to touch. Changing them is a
// sign the agent went wide instead of solving the task. A task that legitimately
// needs one of these, such as a new module bringing its own go.sum, should spell
// out its own list rather than extend this one.
var CommonForbidden = []string{
	".github/",
	".gitlab/",
	"ddtrace/tracer/",
	"go.sum",
}

// GoModuleChecks compiles a Go module, type-checks its tests, and runs them.
//
// `go vet` earns its place separately from `go test`: it type-checks test files,
// so an implementation that does not match what the retained tests expect fails
// here with a clear message instead of somewhere inside a test run.
func GoModuleChecks(dir string) []agenteval.ValidationCommand {
	return []agenteval.ValidationCommand{
		{Label: "build", Command: "cd " + dir + " && go build ./..."},
		{Label: "vet", Command: "cd " + dir + " && go vet ./..."},
		{Label: "tests", Command: "cd " + dir + " && go test ./..."},
	}
}

// RepositoryChecks runs the repository checks that contributors run before
// they submit a new integration. The module check runs first because it can
// change files that the later checks read.
//
// TaskRunner stages the agent changes before validation. The git checks detect
// changes that make fix-modules adds to that staged state.
func RepositoryChecks() []agenteval.ValidationCommand {
	return []agenteval.ValidationCommand{
		{
			Label: "fix_modules",
			Command: "make fix-modules && git diff --quiet && " +
				"test -z \"$(git ls-files --others --exclude-standard)\"",
		},
		{Label: "lint", Command: "make lint"},
		{Label: "tests", Command: "make test"},
	}
}

// Present builds a source check that passes when at least one selected file
// matches the pattern.
func Present(label, pattern string, paths ...string) agenteval.SourceCheck {
	return agenteval.SourceCheck{Label: label, Paths: paths, Pattern: pattern}
}

// Absent builds a source check that passes when no selected file matches. The
// glob must still select something, so deleting the file under scrutiny does not
// pass the check.
func Absent(label, pattern string, paths ...string) agenteval.SourceCheck {
	return agenteval.SourceCheck{Label: label, Paths: paths, Pattern: pattern, Absent: true}
}

// Exists builds a source check that passes when the glob selects any file at
// all. The pattern matches any byte, so what is really being scored is whether
// the glob found something: useful when the exact path cannot be predicted, for
// example an integration test package whose directory the agent gets to name.
func Exists(label string, paths ...string) agenteval.SourceCheck {
	return agenteval.SourceCheck{Label: label, Paths: paths, Pattern: `(?s).`}
}
