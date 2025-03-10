// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package tracer

import (
	"log/slog"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/stretchr/testify/require"
)

func Test_slogHandler(t *testing.T) {
	// Create a record logger to capture the logs and restore the original
	// logger at the end.
	rl := &log.RecordLogger{}
	defer log.UseLogger(rl)()

	// Ensure the logger is set to the default level. This may not be the case
	// when previous tests pollute the global state. We leave the logger in the
	// state we found it to not contribute to this pollution ourselves.
	oldLevel := log.GetLevel()
	log.SetLevel(log.LevelWarn)
	defer log.SetLevel(oldLevel)

	// Log a few messages at different levels. The debug message gets discarded
	// because the internal logger does not have debug enabled by default.
	l := slog.New(slogHandler{})
	l = l.With("foo", "bar")
	l = l.WithGroup("a").WithGroup("b")
	l.Debug("debug test", "n", 0)
	l.Info("info test", "n", 1)
	l.Warn("warn test", "n", 2)
	l.Error("error test", "n", 3)
	log.Flush() // needed to get the error log flushed

	// Check that chaining works as expected.
	l = l.With("baz", "qux")
	l = l.WithGroup("c").WithGroup("d")
	l.Info("info test", "n", 1)

	log.Flush()

	// Check that the logs were written correctly.
	require.Len(t, rl.Logs(), 4)
	require.Contains(t, rl.Logs()[0], "info test foo=bar a.b.n=1")
	require.Contains(t, rl.Logs()[1], "warn test foo=bar a.b.n=2")
	require.Contains(t, rl.Logs()[2], "error test foo=bar a.b.n=3")
	require.Contains(t, rl.Logs()[3], "info test foo=bar a.b.baz=qux a.b.c.d.n=1")
}
