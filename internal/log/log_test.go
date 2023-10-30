// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package log

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// testLogger implements a mock Logger.
type testLogger struct {
	mu    sync.RWMutex
	lines []string
}

// Print implements Logger.
func (tp *testLogger) Log(msg string) {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	tp.lines = append(tp.lines, msg)
}

// Lines returns the lines that were printed using this logger.
func (tp *testLogger) Lines() []string {
	tp.mu.RLock()
	defer tp.mu.RUnlock()
	return tp.lines
}

// Reset resets the logger's internal buffer.
func (tp *testLogger) Reset() {
	tp.mu.Lock()
	tp.lines = tp.lines[:0]
	tp.mu.Unlock()
}

func TestLog(t *testing.T) {
	defer func(old Logger) { UseLogger(old) }(logger)
	tp := &testLogger{}
	UseLogger(tp)

	t.Run("Warn", func(t *testing.T) {
		tp.Reset()
		Warn("message %d", 1)
		assert.Equal(t, msg("WARN", "message 1"), tp.Lines()[0])
	})

	t.Run("Debug", func(t *testing.T) {
		t.Run("on", func(t *testing.T) {
			tp.Reset()
			defer func(old Level) { level = old }(level)
			SetLevel(LevelDebug)
			assert.True(t, DebugEnabled())

			Debug("message %d", 3)
			assert.Equal(t, msg("DEBUG", "message 3"), tp.Lines()[0])
		})

		t.Run("off", func(t *testing.T) {
			tp.Reset()
			assert.False(t, DebugEnabled())
			Debug("message %d", 2)
			assert.Len(t, tp.Lines(), 0)
		})
	})

	t.Run("Error", func(t *testing.T) {
		t.Run("auto", func(t *testing.T) {
			defer func(old time.Duration) { errrate = old }(errrate)
			// Set a long error flush rate so that messages with the
			// same key (format) will be suppressed
			errrate = 10 * time.Hour

			tp.Reset()
			Error("a message %d", 1)
			Error("a message %d", 2)
			Error("a message %d", 3)
			Error("b message")

			Flush()
			assert.True(t, hasMsg("ERROR", "a message 1, 2 additional messages skipped", tp.Lines()), tp.Lines())
			assert.True(t, hasMsg("ERROR", "b message", tp.Lines()), tp.Lines())
			assert.Len(t, tp.Lines(), 2)
		})

		t.Run("flush", func(t *testing.T) {
			tp.Reset()
			Error("fourth message %d", 4)

			Flush()
			assert.True(t, hasMsg("ERROR", "fourth message 4", tp.Lines()), tp.Lines())
			assert.Len(t, tp.Lines(), 1)

			Flush()
			Flush()
			assert.Len(t, tp.Lines(), 1)
		})

		t.Run("limit", func(t *testing.T) {
			tp.Reset()
			for i := 0; i < defaultErrorLimit+1; i++ {
				Error("fifth message %d", i)
			}

			Flush()
			assert.True(t, hasMsg("ERROR", "fifth message 0, 200+ additional messages skipped", tp.Lines()), tp.Lines())
			assert.Len(t, tp.Lines(), 1)
		})

		t.Run("instant", func(t *testing.T) {
			tp.Reset()
			defer func(old time.Duration) { errrate = old }(errrate)
			errrate = time.Duration(0) * time.Second // mimic the env. var.

			Error("fourth message %d", 4)
			assert.True(t, hasMsg("ERROR", "fourth message 4", tp.Lines()), tp.Lines())
			assert.Len(t, tp.Lines(), 1)
		})
	})
}

func TestRecordLoggerIgnore(t *testing.T) {
	tp := new(RecordLogger)
	tp.Ignore("appsec")
	tp.Log("this is an appsec log")
	tp.Log("this is a tracer log")
	assert.Len(t, tp.Logs(), 1)
	assert.NotContains(t, tp.Logs()[0], "appsec")
	tp.Reset()
	tp.Log("this is an appsec log")
	assert.Len(t, tp.Logs(), 1)
	assert.Contains(t, tp.Logs()[0], "appsec")
}

func TestSetLoggingRate(t *testing.T) {
	testCases := []struct {
		input  string
		result time.Duration
	}{
		{
			input:  "",
			result: time.Minute,
		},
		{
			input:  "0",
			result: 0 * time.Second,
		},
		{
			input:  "10",
			result: 10 * time.Second,
		},
		{
			input:  "-1",
			result: time.Minute,
		},
		{
			input:  "this is not a number",
			result: time.Minute,
		},
	}
	for _, tC := range testCases {
		tC := tC
		errrate = time.Minute // reset global variable
		t.Run(tC.input, func(t *testing.T) {
			setLoggingRate(tC.input)
			assert.Equal(t, tC.result, errrate)
		})
	}
}

func BenchmarkError(b *testing.B) {
	Error("k %s", "a") // warm up cache
	for i := 0; i < b.N; i++ {
		Error("k %s", "a")
	}
}

func hasMsg(lvl, m string, lines []string) bool {
	for _, line := range lines {
		if strings.HasPrefix(line, msg(lvl, m)) {
			return true
		}
	}
	return false
}

func msg(lvl, msg string) string {
	return fmt.Sprintf("%s %s: %s", prefixMsg, lvl, msg)
}
