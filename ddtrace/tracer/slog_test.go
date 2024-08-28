package tracer

import (
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

func Test_slogHandler(t *testing.T) {
	// Create a record logger to capture the logs and restore the original
	// logger at the end.
	rl := &log.RecordLogger{}
	defer log.UseLogger(rl)()

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

	// Check that the logs were written correctly.
	require.Len(t, rl.Logs(), 3)
	require.Contains(t, rl.Logs()[0], "info test foo=bar a.b.n=1")
	require.Contains(t, rl.Logs()[1], "warn test foo=bar a.b.n=2")
	require.Contains(t, rl.Logs()[2], "error test foo=bar a.b.n=3")
}