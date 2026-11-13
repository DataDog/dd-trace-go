// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux || !githubci

package zerolog

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/require"

	zerologtrace "github.com/DataDog/dd-trace-go/contrib/rs/zerolog/v2"

	"github.com/DataDog/dd-trace-go/v2/internal/otelc"
)

// TestCaseCorrelationNew covers a logger built with zerolog.New.
type TestCaseCorrelationNew struct {
	correlation
}

func (tc *TestCaseCorrelationNew) Setup(context.Context, *testing.T) {
	tc.logger = zerolog.New(tc.newLogs())
}

var (
	pkgLogs   bytes.Buffer
	pkgLogger = zerolog.New(&pkgLogs)
)

// TestCaseCorrelationPackageVar covers a logger built with zerolog.New in a
// package-level var, during package initialization.
type TestCaseCorrelationPackageVar struct {
	correlation
}

func (tc *TestCaseCorrelationPackageVar) Setup(context.Context, *testing.T) {
	pkgLogs.Reset()
	tc.logs = &pkgLogs
	tc.logger = pkgLogger
}

// TestCaseCorrelationOutput covers Logger.Output, which builds a new logger
// with zerolog.New inside zerolog and copies the hooks of the original.
// Orchestrion adds its hook in zerolog.New too, so the logger holds two.
type TestCaseCorrelationOutput struct {
	correlation
}

func (tc *TestCaseCorrelationOutput) Setup(_ context.Context, t *testing.T) {
	if !otelc.Enabled() {
		t.Skip("orchestrion adds a second hook in the zerolog.New call of Output")
	}
	tc.logger = zerolog.New(io.Discard).Output(tc.newLogs())
}

// TestCaseCorrelationGlobal covers the global logger, which zerolog/log builds
// with zerolog.New in a package-level var. Output is the only way to read its
// events, so orchestrion adds a second hook here too.
type TestCaseCorrelationGlobal struct {
	correlation
}

func (tc *TestCaseCorrelationGlobal) Setup(_ context.Context, t *testing.T) {
	require.Equal(t, 1, contextLogHooks(log.Logger), "DDContextLogHook count of log.Logger")
	if !otelc.Enabled() {
		t.Skip("orchestrion adds a second hook in the zerolog.New call of Output")
	}
	tc.logger = log.Output(tc.newLogs())
}

// TestCaseCorrelationContribHook covers a logger the application also sets up
// with the contrib's hook. It holds two hooks and every event still gets the
// trace fields once. Orchestrion adds them twice.
type TestCaseCorrelationContribHook struct {
	correlation
}

func (tc *TestCaseCorrelationContribHook) Setup(_ context.Context, t *testing.T) {
	if !otelc.Enabled() {
		t.Skip("orchestrion adds the trace fields once per hook")
	}
	tc.logger = zerolog.New(tc.newLogs()).Hook(&zerologtrace.DDContextLogHook{})
	tc.hooks = 2
}
