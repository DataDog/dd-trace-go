// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package log

import (
	"bytes"
	"fmt"
	"os"
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

var _ Logger = &testLogger{}

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

func TestLogDirectory(t *testing.T) {
	t.Run("invalid", func(t *testing.T) {
		f, err := OpenFileAtPath("/some/nonexistent/path")
		assert.Nil(t, f)
		assert.Error(t, err)
	})
	t.Run("valid", func(t *testing.T) {
		// ensure File is created successfully
		dir, err := os.MkdirTemp("", "example")
		if err != nil {
			t.Fatalf("Failure creating directory %v", err)
		}
		f, err := OpenFileAtPath(dir)
		assert.Nil(t, err)
		fp := dir + "/" + LoggerFile
		assert.NotNil(t, f.file)
		assert.Equal(t, fp, f.file.Name())
		assert.False(t, f.closed)

		// ensure this setting plays nicely with other log features
		oldLvl := levelThreshold
		SetLevel(LevelDebug)
		defer func() {
			SetLevel(oldLvl)
		}()
		Info("info!")
		Warn("warn!")
		Debug("debug!")
		// shorten errrate to test Error() behavior in a reasonable amount of time
		oldRate := errrate
		errrate = time.Microsecond
		defer func() {
			errrate = oldRate
		}()
		Error("error!")
		time.Sleep(1 * time.Second)

		b, err := os.ReadFile(fp)
		if err != nil {
			t.Fatalf("Failure reading file: %v", err)
		}
		// convert file content to []string{}, split by \n, to easily check its contents
		lines := bytes.Split(b, []byte{'\n'})
		var logs []string
		for _, line := range lines {
			logs = append(logs, string(line))
		}

		assert.True(t, containsMessage("INFO", "info!", logs))
		assert.True(t, containsMessage("WARN", "warn!", logs))
		assert.True(t, containsMessage("DEBUG", "debug!", logs))
		assert.True(t, containsMessage("ERROR", "error!", logs))

		f.Close()
		assert.True(t, f.closed)

		//ensure f.Close() is concurrent-safe and free of deadlocks
		var wg sync.WaitGroup
		for i := 0; i < 100; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				f.Close()
			}()
		}
		wg.Wait()
		assert.True(t, f.closed)
	})
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
			defer func(old Level) { levelThreshold = old }(levelThreshold)
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

func containsMessage(lvl, m string, lines []string) bool {
	for _, line := range lines {
		if strings.Contains(line, msg(lvl, m)) {
			return true
		}
	}
	return false
}

func BenchmarkLog(b *testing.B) {
	UseLogger(DiscardLogger{})
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		Warn("test")
	}
}
