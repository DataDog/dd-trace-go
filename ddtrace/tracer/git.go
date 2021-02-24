// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2020 Datadog, Inc.

package tracer

import "runtime/debug"

// Values can be injected at build time.
var (
	RepositoryURL string
	CommitHash    string
)

func Stack() string {
	return string(debug.Stack())
}

func Panic() {
	panic("panic")
}

var (
	repository string
	commitHash string
)

func ReportUnexportedGitInfo() (string, string) {
	return repository, commitHash
}
