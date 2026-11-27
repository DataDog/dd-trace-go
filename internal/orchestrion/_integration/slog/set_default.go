// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package slog

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
	"github.com/DataDog/dd-trace-go/v2/internal/otelc"
)

// TestCaseSetDefault checks the records of a logger the application makes the
// default one with slog.SetDefault.
type TestCaseSetDefault struct{}

func (*TestCaseSetDefault) Setup(context.Context, *testing.T) {}

func (*TestCaseSetDefault) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "slog.test")
	defer span.Finish()

	var logs bytes.Buffer
	withDefault(t, slog.New(slog.NewJSONHandler(&logs, nil)), func() {
		slog.InfoContext(ctx, "package-func")
		slog.Default().WarnContext(ctx, "default")
	})

	records := parseRecords(t, logs.Bytes())
	require.Len(t, records, 2)
	requireCorrelated(t, records["package-func"], span)
	requireCorrelated(t, records["default"], span)
}

func (*TestCaseSetDefault) ExpectedSpanCount() int { return 1 }

func (*TestCaseSetDefault) ExpectedTraces() trace.Traces {
	return trace.Traces{{Tags: map[string]any{"name": "slog.test"}}}
}

// TestCaseLogBridge checks that slog.SetDefault still sends the output of the
// log package to the new default handler. Orchestrion changes SetDefault to
// skip this for every handler it wraps, so it only runs under otelc.
type TestCaseLogBridge struct{}

func (*TestCaseLogBridge) Setup(_ context.Context, t *testing.T) {
	if !otelc.Enabled() {
		t.Skip("orchestrion's SetDefault does not send the log package output to a handler it wrapped")
	}
}

func (*TestCaseLogBridge) Run(_ context.Context, t *testing.T) {
	var logs bytes.Buffer
	withDefault(t, slog.New(slog.NewJSONHandler(&logs, nil)), func() {
		log.Print("log-package")
	})

	records := parseRecords(t, logs.Bytes())
	require.Len(t, records, 1)
	requireNotCorrelated(t, records["log-package"])
}

func (*TestCaseLogBridge) ExpectedSpanCount() int { return 0 }

func (*TestCaseLogBridge) ExpectedTraces() trace.Traces { return trace.Traces{} }

// TestCaseDefaultHandler checks that a logger around the default handler of
// log/slog can become the default logger. That handler writes through the log
// package, so slog.SetDefault must not send the log package output back to it,
// or the first log.Print blocks forever.
type TestCaseDefaultHandler struct{}

func (*TestCaseDefaultHandler) Setup(context.Context, *testing.T) {}

func (*TestCaseDefaultHandler) Run(_ context.Context, t *testing.T) {
	for name, handler := range map[string]func() slog.Handler{
		"default":    func() slog.Handler { return slog.Default().Handler() },
		"with attrs": func() slog.Handler { return slog.Default().Handler().WithAttrs([]slog.Attr{slog.String("k", "v")}) },
	} {
		t.Run(name, func(t *testing.T) {
			var logs bytes.Buffer
			writer := log.Writer()
			log.SetOutput(&logs)

			done := make(chan struct{})
			go func(l *slog.Logger) {
				defer close(done)
				withDefault(t, l, func() {
					log.Print("log-package")
				})
			}(slog.New(handler()))
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				// The blocked goroutine holds the mutex of the log package, so any
				// later log call blocks too, deferred and cleanup functions
				// included. Only exiting stops the test binary.
				fmt.Fprintln(os.Stderr, "log.Print blocked: the log package output went back to the default handler")
				os.Exit(1)
			}
			log.SetOutput(writer)
			require.Contains(t, logs.String(), "log-package")
		})
	}
}

func (*TestCaseDefaultHandler) ExpectedSpanCount() int { return 0 }

func (*TestCaseDefaultHandler) ExpectedTraces() trace.Traces { return trace.Traces{} }

// withDefault runs f with l as the default logger, then restores the default
// logger and the output of the log package, which SetDefault changes.
func withDefault(t *testing.T, l *slog.Logger, f func()) {
	t.Helper()
	orig, writer, flags := slog.Default(), log.Writer(), log.Flags()
	defer func() {
		slog.SetDefault(orig)
		log.SetOutput(writer)
		log.SetFlags(flags)
	}()
	slog.SetDefault(l)
	f()
}
