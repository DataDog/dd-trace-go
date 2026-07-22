// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testutils

import (
	"runtime"
	"strings"
	"testing"
)

// SkipIfGoTip skips t when the test binary is built with an unreleased
// ("devel") Go toolchain — e.g. the nightly gotip CI job — where unexported
// standard-library layout can drift ahead of released Go. On a released
// toolchain it is a no-op and the test proceeds.
//
// runtime.Version() reports "devel go1.NN-<hash> <date>" for such toolchains,
// versus "go1.NN.P" for a release; the "devel" prefix is the sentinel.
//
// The optional format/args are logged (like t.Logf) before the skip so call
// sites keep their diagnostics; the skip message always names the toolchain.
// Use it in place of a hard t.Fatal when an assertion depends on standard
// library internals that legitimately drift on tip.
func SkipIfGoTip(t testing.TB, format string, args ...any) {
	t.Helper()
	v := runtime.Version()
	if !versionIsGoTip(v) {
		return
	}
	if format != "" {
		t.Logf(format, args...)
	}
	t.Skipf("skipping on unreleased Go toolchain %s", v)
}

// versionIsGoTip reports whether v, a runtime.Version() string, denotes an
// unreleased ("devel") toolchain.
func versionIsGoTip(v string) bool {
	return strings.HasPrefix(v, "devel")
}
