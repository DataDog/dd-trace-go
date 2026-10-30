// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package connectshared

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/DataDog/dd-trace-go/v2/internal/log"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testCallFinish classifies the call span of the bidi streams of these tests.
var testCallFinish = SpanFinish{Procedure: bidiProcedure, Protocol: &connectProtocol}

// newTestClientCall returns the state of a bidi stream whose call span is span, or that has none
// if span is nil, configured with setters. Its context never ends, so tests run the context
// watcher themselves, with requestFinish.
func newTestClientCall(span *tracer.Span, setters ...func(*Config)) *ClientCall {
	call := new(ClientCall)
	call.Init(context.Background(), newTestConfig(setters...), span, testCallFinish)
	return call
}

func enableMetadataTags(c *Config) { c.EnableMetadataTags() }

// startedWatcher gives c a context watcher in the state it is in once the context is done: stopping
// it fails, so the operation that claims the finish waits for it to return. stopCalled is closed
// when that operation tries to stop it, and run runs it with the context's error err.
func startedWatcher(c *ClientCall) (stopCalled <-chan struct{}, run func(err error)) {
	stopped := make(chan struct{})
	c.watcherDone.Add(1)
	c.stopContextDone = func() bool {
		close(stopped)
		return false
	}
	return stopped, func(err error) {
		defer c.watcherDone.Done()
		c.requestFinish(err)
	}
}

// TestClientCallTerminalError drives the bookkeeping of streams whose operations and context race
// to end the stream, and checks which error the call span records.
func TestClientCallTerminalError(t *testing.T) {
	canceled := func(message string) error { return newTestError(CodeCanceled, message) }
	internal := func(message string) error { return newTestError(CodeInternal, message) }
	var (
		rejected  = internal("flaky, ignore")
		rejected2 = internal("flaky again")
		accepted  = internal("real failure")
		shadowed  = internal("shadowed")
	)
	rejects := func(errs ...error) func(int32, error) bool {
		return func(_ int32, err error) bool { return !slices.Contains(errs, err) }
	}
	for _, test := range []struct {
		name string
		// errCheck is called with the number of calls so far, including this one.
		errCheck   func(calls int32, err error) bool
		noCallSpan bool
		run        func(*ClientCall)
		wantCode   any
		// wantError is a substring of the recorded error; empty if none may be recorded.
		wantError      string
		wantCheckCalls int32 // checked if positive, or with noCallSpan
	}{
		{
			name: "real error beats an earlier cancellation",
			run: func(c *ClientCall) {
				c.Begin()
				c.requestFinish(context.Canceled)
				c.End(newTestError(codeUnavailable, "boom"), false, true, false)
			},
			wantCode: "unavailable", wantError: "boom",
		},
		{
			name: "panic displaces a stored error",
			run: func(c *ClientCall) {
				c.Begin()
				c.Begin()
				c.End(canceled("canceled"), false, true, false)
				c.End(errors.New("boom"), true, true, false)
			},
			wantCode: "unknown", wantError: "boom",
		},
		{
			name: "panic is never displaced",
			run: func(c *ClientCall) {
				c.Begin()
				c.Begin()
				c.End(errors.New("boom"), true, true, false)
				c.End(internal("later"), false, true, false)
			},
			wantCode: "unknown", wantError: "boom",
		},
		{
			name: "panic ends the stream, even with the end of the stream",
			run: func(c *ClientCall) {
				c.Begin()
				c.End(io.EOF, true, false, false)
			},
			wantCode: "unknown", wantError: "EOF",
		},
		{
			name: "real error replaces a suppressed one",
			run: func(c *ClientCall) {
				c.Begin()
				c.Begin()
				c.End(canceled("send canceled"), false, true, false)
				c.End(internal("boom"), false, true, false)
			},
			wantCode: "internal", wantError: "boom",
		},
		{
			// Were the rejected error only a fallback, the status would stay canceled.
			name:     "rejected real error still replaces a suppressed one",
			errCheck: rejects(rejected),
			run: func(c *ClientCall) {
				c.Begin()
				c.Begin()
				c.End(canceled("send canceled"), false, true, false)
				c.End(rejected, false, true, false)
			},
			wantCode: "internal", wantCheckCalls: 1,
		},
		{
			name: "context deadline replaces a suppressed error",
			run: func(c *ClientCall) {
				c.Begin()
				c.Begin()
				c.End(canceled("send canceled"), false, true, false)
				c.requestFinish(context.DeadlineExceeded)
				// The in-flight operation ends without ending the stream itself.
				c.End(nil, false, false, false)
			},
			wantCode: "deadline_exceeded", wantError: "deadline exceeded",
		},
		{
			name: "context deadline survives a later suppressed error",
			run: func(c *ClientCall) {
				c.Begin()
				c.Begin()
				c.requestFinish(context.DeadlineExceeded)
				c.End(canceled("receive canceled"), false, true, false)
				c.End(nil, false, false, false)
			},
			wantCode: "deadline_exceeded", wantError: "deadline exceeded",
		},
		{
			name:     "error check runs once for a suppressed then a real error",
			errCheck: func(int32, error) bool { return true },
			run: func(c *ClientCall) {
				c.Begin()
				c.Begin()
				c.End(canceled("send canceled"), false, true, false)
				c.End(internal("boom"), false, true, false)
			},
			wantCode: "internal", wantError: "boom", wantCheckCalls: 1,
		},
		{
			name:     "falls back when the error check rejects the first real error",
			errCheck: rejects(rejected),
			run: func(c *ClientCall) {
				c.Begin()
				c.Begin()
				c.End(rejected, false, true, false)
				c.End(accepted, false, true, false)
			},
			wantCode: "internal", wantError: "real failure", wantCheckCalls: 2,
		},
		{
			name:     "keeps the first real error when the error check rejects the second",
			errCheck: rejects(rejected),
			run: func(c *ClientCall) {
				c.Begin()
				c.Begin()
				c.End(accepted, false, true, false)
				c.End(rejected, false, true, false)
			},
			wantCode: "internal", wantError: "real failure", wantCheckCalls: 1,
		},
		{
			// A stateful check would flip its verdict if the primary error were checked twice.
			name:     "error check runs once for the primary error",
			errCheck: func(calls int32, _ error) bool { return calls == 1 },
			run: func(c *ClientCall) {
				c.Begin()
				c.Begin()
				c.End(accepted, false, true, false)
				c.End(shadowed, false, true, false)
			},
			wantCode: "internal", wantError: "real failure", wantCheckCalls: 1,
		},
		{
			name:     "three-way race keeps the accepted fallback",
			errCheck: rejects(rejected, rejected2),
			run: func(c *ClientCall) {
				c.Begin()
				c.Begin()
				c.End(rejected, false, true, false)
				c.requestFinish(context.DeadlineExceeded)
				c.End(rejected2, false, true, false)
			},
			wantCode: "deadline_exceeded", wantError: "deadline exceeded",
		},
		{
			name:     "error check rejecting every error keeps the status",
			errCheck: rejects(rejected, rejected2),
			run: func(c *ClientCall) {
				c.Begin()
				c.Begin()
				c.End(rejected, false, true, false)
				c.End(rejected2, false, true, false)
			},
			wantCode: "internal", wantCheckCalls: 2,
		},
		{
			name:       "no call span skips classification",
			errCheck:   func(int32, error) bool { return true },
			noCallSpan: true,
			run: func(c *ClientCall) {
				c.Begin()
				c.End(internal("boom"), false, true, false)
			},
		},
		{
			name: "kept EOF is an error",
			run: func(c *ClientCall) {
				c.Begin()
				c.End(io.EOF, false, true, true)
			},
			wantCode: "unknown", wantError: "EOF",
		},
		{
			name: "kept EOF replaces a suppressed error",
			run: func(c *ClientCall) {
				c.Begin()
				c.Begin()
				c.End(canceled("receive canceled"), false, true, false)
				c.End(io.EOF, false, true, true)
			},
			wantCode: "unknown", wantError: "EOF",
		},
		{
			name:     "error check sees a kept EOF",
			errCheck: rejects(io.EOF),
			run: func(c *ClientCall) {
				c.Begin()
				c.End(io.EOF, false, true, true)
			},
			wantCode: "unknown", wantCheckCalls: 1,
		},
		{
			name: "end of stream",
			run: func(c *ClientCall) {
				c.Begin()
				c.End(io.EOF, false, true, false)
			},
		},
		{
			name: "only the last operation in flight finishes the span",
			run: func(c *ClientCall) {
				c.Begin()
				c.Begin()
				c.End(internal("boom"), false, true, false)
				c.Begin()
				c.End(nil, false, false, false)
				c.End(nil, false, false, false)
			},
			wantCode: "internal", wantError: "boom",
		},
		// The end of the context stands for an abandoned stream. A Receive that was running then
		// and returns the end of the response shows that the caller has the RPC's result.
		{
			name: "the end of the stream in flight drops the context's deadline",
			run: func(c *ClientCall) {
				c.Begin()
				c.requestFinish(context.DeadlineExceeded)
				c.EndReceive(io.EOF, false, false)
			},
		},
		{
			name: "the end of the stream in flight drops the context's cancellation",
			run: func(c *ClientCall) {
				c.Begin()
				c.requestFinish(context.Canceled)
				c.EndReceive(io.EOF, false, false)
			},
		},
		{
			name: "the single response in flight drops the context's deadline",
			run: func(c *ClientCall) {
				c.Begin()
				c.requestFinish(context.DeadlineExceeded)
				c.EndReceive(nil, false, true)
			},
		},
		{
			name: "the context ending after the end of the stream records nothing",
			run: func(c *ClientCall) {
				c.Begin()
				c.Begin()
				c.EndReceive(io.EOF, false, false)
				c.requestFinish(context.DeadlineExceeded)
				c.End(nil, false, false, false)
			},
		},
		{
			name: "a message in flight keeps the context's deadline",
			run: func(c *ClientCall) {
				c.Begin()
				c.requestFinish(context.DeadlineExceeded)
				c.EndReceive(nil, false, false)
			},
			wantCode: "deadline_exceeded", wantError: "deadline exceeded",
		},
		{
			name: "a missing single response in flight keeps the context's deadline",
			run: func(c *ClientCall) {
				c.Begin()
				c.requestFinish(context.DeadlineExceeded)
				c.EndReceive(io.EOF, false, true)
			},
			wantCode: "deadline_exceeded", wantError: "deadline exceeded",
		},
		{
			name: "the context's deadline comes before a later Receive error",
			run: func(c *ClientCall) {
				c.Begin()
				c.requestFinish(context.DeadlineExceeded)
				c.EndReceive(internal("boom"), false, false)
			},
			wantCode: "deadline_exceeded", wantError: "deadline exceeded",
		},
		{
			name: "a panic in flight displaces the context's deadline",
			run: func(c *ClientCall) {
				c.Begin()
				c.requestFinish(context.DeadlineExceeded)
				c.EndReceive(errors.New("boom"), true, false)
			},
			wantCode: "unknown", wantError: "boom",
		},
		{
			name:       "no call span skips the classification of a Receive",
			errCheck:   func(int32, error) bool { return true },
			noCallSpan: true,
			run: func(c *ClientCall) {
				c.Begin()
				c.EndReceive(internal("boom"), false, false)
			},
		},
		// A result set while the stream runs is what the caller got, however the stream ends.
		{
			name: "the result replaces the terminal error",
			run: func(c *ClientCall) {
				c.Begin()
				c.SetResult(internal("result"))
				c.End(nil, false, false, false)
				c.Begin()
				c.End(newTestError(codeUnavailable, "boom"), false, true, false)
			},
			wantCode: "internal", wantError: "result",
		},
		{
			name: "the result replaces the end of the stream",
			run: func(c *ClientCall) {
				c.Begin()
				c.SetResult(internal("result"))
				c.EndReceive(io.EOF, false, false)
			},
			wantCode: "internal", wantError: "result",
		},
		{
			name: "the result replaces the context's deadline",
			run: func(c *ClientCall) {
				c.Begin()
				c.SetResult(internal("result"))
				c.End(nil, false, false, false)
				c.requestFinish(context.DeadlineExceeded)
			},
			wantCode: "internal", wantError: "result",
		},
		{
			name: "a panic displaces the result",
			run: func(c *ClientCall) {
				c.Begin()
				c.SetResult(internal("result"))
				c.End(nil, false, false, false)
				c.Begin()
				c.End(errors.New("boom"), true, true, false)
			},
			wantCode: "unknown", wantError: "boom",
		},
		{
			name:     "the error check rejecting the result leaves no fallback",
			errCheck: rejects(rejected),
			run: func(c *ClientCall) {
				c.Begin()
				c.Begin()
				c.End(accepted, false, true, false)
				c.SetResult(rejected)
				c.End(nil, false, false, false)
			},
			wantCode: "internal", wantCheckCalls: 1,
		},
		{
			name: "a nil result undoes the result",
			run: func(c *ClientCall) {
				c.Begin()
				c.SetResult(internal("result"))
				c.End(nil, false, false, false)
				c.Begin()
				c.SetResult(nil)
				c.EndReceive(io.EOF, false, false)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			mt := startMockTracer(t)
			var calls atomic.Int32
			var setters []func(*Config)
			if test.errCheck != nil {
				setters = append(setters, errorCheck(func(_ string, err error) bool {
					return test.errCheck(calls.Add(1), err)
				}))
			}
			var span *tracer.Span
			if !test.noCallSpan {
				span = tracer.StartSpan(operationClient)
			}
			c := newTestClientCall(span, setters...)
			require.NotPanics(t, func() { test.run(c) })
			if test.wantCheckCalls > 0 || test.noCallSpan {
				assert.Equal(t, test.wantCheckCalls, calls.Load(), "error check calls")
			}
			if test.noCallSpan {
				assert.Empty(t, mt.FinishedSpans())
				return
			}
			finished := requireFinished(t, mt)
			assert.Equal(t, test.wantCode, finished.Tag(tagConnectErrorCode))
			if test.wantError == "" {
				assert.Nil(t, finished.Tag(ext.ErrorMsg))
			} else {
				assert.Contains(t, finished.Tag(ext.ErrorMsg), test.wantError)
			}
		})
	}

	// An application may keep using a stream whose call span has finished.
	t.Run("errors after the finish are not kept", func(t *testing.T) {
		mt := startMockTracer(t)
		c := newTestClientCall(tracer.StartSpan(operationClient))
		c.Begin()
		c.End(internal("first"), false, true, false)
		require.Len(t, mt.FinishedSpans(), 1)
		for range 100 {
			c.Begin()
			c.End(internal("later"), false, true, false)
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		assert.Empty(t, c.fallbacks)
		assert.Equal(t, internal("first").Error(), c.terminal.err.Error())
	})

	t.Run("the call's finish mode classifies the terminal error", func(t *testing.T) {
		mt := startMockTracer(t)
		c := new(ClientCall)
		f := SpanFinish{Procedure: unaryProcedure, Protocol: &connectProtocol, Mode: FinishMode{allowNotModified: true}}
		c.Init(context.Background(), newTestConfig(), tracer.StartSpan(operationClient), f)
		c.Begin()
		c.Begin()
		c.End(notModifiedError(), false, true, false)
		// A real error replaces the suppressed not-modified error.
		c.End(newTestError(CodeInternal, "boom"), false, true, false)
		finished := requireFinished(t, mt)
		assert.Equal(t, "internal", finished.Tag(tagConnectErrorCode))
		assert.Nil(t, finished.Tag(ext.HTTPCode))

		mt.Reset()
		c = new(ClientCall)
		c.Init(context.Background(), newTestConfig(), tracer.StartSpan(operationClient), f)
		c.Begin()
		c.End(notModifiedError(), false, true, false)
		finished = requireFinished(t, mt)
		assert.Equal(t, "304", finished.Tag(ext.HTTPCode))
		assert.Nil(t, finished.Tag(ext.ErrorMsg))
	})
}

// crashChildEnv makes TestClientCallUnrecoveredErrorCheckPanic, run again in a child process, crash
// that process.
const crashChildEnv = "CONNECTSHARED_TEST_CRASH_CHILD"

// TestClientCallUnrecoveredErrorCheckPanic checks how a panic in the error check that nothing
// recovers ends the program: the runtime reports that panic, not a recovered and re-raised one.
func TestClientCallUnrecoveredErrorCheckPanic(t *testing.T) {
	if os.Getenv(crashChildEnv) != "" {
		startMockTracer(t)
		c := newTestClientCall(tracer.StartSpan(operationClient), errorCheck(func(string, error) bool { panic("errCheck panic") }))
		c.Begin()
		ended := make(chan struct{})
		// The goroutine running the test recovers and re-raises panics itself.
		go func() {
			c.End(newTestError(CodeInternal, "boom"), false, true, false)
			close(ended)
		}()
		<-ended
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^"+t.Name()+"$")
	cmd.Env = append(os.Environ(), crashChildEnv+"=1")
	out, err := cmd.CombinedOutput()
	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr, "the child process did not crash:\n%s", out)
	var report string
	for line := range strings.Lines(string(out)) {
		if strings.HasPrefix(line, "panic: ") {
			report = strings.TrimSuffix(line, "\n")
			break
		}
	}
	assert.Equal(t, "panic: errCheck panic", report, "child process output:\n%s", out)
}

func TestClientCallContextWatcher(t *testing.T) {
	for _, test := range []struct {
		name string
		end  func(*ClientCall)
		// wantCode is the recorded code, with the context's deadline as the watcher's error; nil if
		// no error may be recorded.
		wantCode any
	}{
		{
			// Finishing with this suppressed error without waiting for the watcher would record no
			// error.
			name:     "an operation ending the stream waits for a running watcher",
			end:      func(c *ClientCall) { c.End(newTestError(CodeCanceled, "send canceled"), false, true, false) },
			wantCode: "deadline_exceeded",
		},
		{
			name: "the end of the response waits for a running watcher, which records nothing",
			end:  func(c *ClientCall) { c.EndReceive(io.EOF, false, false) },
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			mt := startMockTracer(t)
			c := newTestClientCall(tracer.StartSpan(operationClient))
			stopCalled, runWatcher := startedWatcher(c)
			c.Begin()

			endDone := make(chan struct{})
			go func() {
				defer close(endDone)
				test.end(c)
			}()
			select {
			case <-stopCalled:
			case <-time.After(5 * time.Second):
				t.Fatal("the operation did not stop the context watcher")
			}
			select {
			case <-endDone:
				t.Fatal("the operation did not wait for the context watcher")
			case <-time.After(20 * time.Millisecond):
			}
			runWatcher(context.DeadlineExceeded)
			select {
			case <-endDone:
			case <-time.After(5 * time.Second):
				t.Fatal("the operation did not resume after the context watcher returned")
			}

			finished := requireFinished(t, mt)
			assert.Equal(t, test.wantCode, finished.Tag(tagConnectErrorCode))
			assert.Equal(t, test.wantCode != nil, finished.Tag(ext.ErrorMsg) != nil, "error: %v", finished.Tag(ext.ErrorMsg))
		})
	}

	// The watcher runs in its own goroutine, where a panic would crash the process.
	t.Run("recovers an error check panic", func(t *testing.T) {
		mt := startMockTracer(t)
		c := newTestClientCall(tracer.StartSpan(operationClient), errorCheck(func(string, error) bool { panic("errCheck panic") }))
		require.NotPanics(t, func() {
			c.requestFinish(newTestError(CodeInternal, "boom"))
		})
		assert.NotNil(t, requireFinished(t, mt).Tag(ext.ErrorMsg))
	})

	t.Run("recovers an error check panic whose value's Error method panics", func(t *testing.T) {
		mt := startMockTracer(t)
		c := newTestClientCall(tracer.StartSpan(operationClient), errorCheck(func(string, error) bool {
			panic(&formatPanicError{value: "Error called"})
		}))
		assert.Equal(t, callOutcome{}, outcomeOf(t, func() { c.requestFinish(newTestError(CodeInternal, "boom")) }))
		assert.Contains(t, requireFinished(t, mt).Tag(ext.ErrorMsg), "panics when formatted")
	})

	t.Run("recovers an error check panic(nil)", func(t *testing.T) {
		if !runsWithPanicNil(t) {
			return
		}
		mt := startMockTracer(t)
		logs := new(log.RecordLogger)
		defer log.UseLogger(logs)()
		log.Flush()
		logs.Reset()
		c := newTestClientCall(tracer.StartSpan(operationClient), errorCheck(func(string, error) bool { panicWith(nil); return true }))
		assert.Equal(t, callOutcome{}, outcomeOf(t, func() { c.requestFinish(newTestError(CodeInternal, "boom")) }))
		assert.Equal(t, "panic: <nil>", requireFinished(t, mt).Tag(ext.ErrorMsg))
		log.Flush()
		assert.True(t, slices.ContainsFunc(logs.Logs(), func(line string) bool {
			return strings.Contains(line, logPrefix+": recovered panic finishing stream after context cancellation: <nil>")
		}), "logs: %q", logs.Logs())
	})

	// The logger aggregates errors by format string, so a process using both integrations must
	// still report each one's panics.
	t.Run("logs the recovered panics of each integration apart", func(t *testing.T) {
		startMockTracer(t)
		logs := new(log.RecordLogger)
		defer log.UseLogger(logs)()
		log.Flush()
		logs.Reset()
		prefixes := map[instrumentation.Package]string{
			instrumentation.PackageConnectRPC:   "contrib/connectrpc.com/connect",
			instrumentation.PackageConnectRPCV2: "contrib/connectrpc.com/connect.v2",
		}
		for pkg, prefix := range prefixes {
			cfg := NewConfig(testInstr[pkg], pkg, &testLibrary)
			cfg.SetErrorCheck(func(string, error) bool { panic("errCheck panic in " + prefix) })
			cfg.Init()
			c := new(ClientCall)
			c.Init(context.Background(), cfg, tracer.StartSpan(operationClient), testCallFinish)
			c.requestFinish(newTestError(CodeInternal, "boom"))
		}
		log.Flush()
		var reports []string
		for _, line := range logs.Logs() {
			if strings.Contains(line, "recovered panic finishing stream") {
				reports = append(reports, line)
			}
		}
		require.Len(t, reports, len(prefixes), "reports: %q", reports)
		for _, prefix := range prefixes {
			want := prefix + ": recovered panic finishing stream after context cancellation: errCheck panic in " + prefix + " (occurred: "
			assert.True(t, slices.ContainsFunc(reports, func(report string) bool { return strings.Contains(report, want) }),
				"no report %q in %q", want, reports)
		}
	})

	t.Run("the end of the context finishes the call span", func(t *testing.T) {
		mt := startMockTracer(t)
		ctx, cancel := context.WithCancel(context.Background())
		c := new(ClientCall)
		c.Init(ctx, newTestConfig(), tracer.StartSpan(operationClient), testCallFinish)
		cancel()
		require.Eventually(t, func() bool { return len(mt.FinishedSpans()) == 1 }, 5*time.Second, time.Millisecond)
		finished := mt.FinishedSpans()[0]
		assert.Equal(t, "canceled", finished.Tag(tagConnectErrorCode))
		assert.Nil(t, finished.Tag(ext.ErrorMsg))
	})

	t.Run("an operation in flight finishes the call span after the context ends", func(t *testing.T) {
		mt := startMockTracer(t)
		ctx, cancel := context.WithCancel(context.Background())
		c := new(ClientCall)
		c.Init(ctx, newTestConfig(), tracer.StartSpan(operationClient), testCallFinish)
		c.Begin()
		cancel()
		c.watcherDone.Wait()
		assert.Empty(t, mt.FinishedSpans())
		c.End(nil, false, false, false)
		assert.Equal(t, "canceled", requireFinished(t, mt).Tag(tagConnectErrorCode))
	})

	t.Run("without a watcher, only the operations end the stream", func(t *testing.T) {
		mt := startMockTracer(t)
		c := new(ClientCall)
		c.Init(context.Background(), newTestConfig(), tracer.StartSpan(operationClient), testCallFinish)
		assert.Nil(t, c.stopContextDone)
		assert.True(t, c.Begin())
		assert.Empty(t, mt.FinishedSpans())
		c.End(nil, false, true, false)
		finished := requireFinished(t, mt)
		assert.Nil(t, finished.Tag(tagConnectErrorCode))
		assert.Nil(t, finished.Tag(ext.ErrorMsg))
	})

	t.Run("without a call span, there is no watcher", func(t *testing.T) {
		c := new(ClientCall)
		c.Init(t.Context(), newTestConfig(), nil, testCallFinish)
		assert.Nil(t, c.stopContextDone)
		assert.Nil(t, c.ctx)
	})
}

func TestClientCallMetadata(t *testing.T) {
	header := map[string][]string{"X-Meta": {"early"}, "Authorization": {"secret"}}
	wantTags := map[string]any{"rpc.request.metadata.x-meta.0": "early"}

	t.Run("disabled", func(t *testing.T) {
		mt := startMockTracer(t)
		c := newTestClientCall(nil)
		claimed, span, keep := c.ClaimMetadata(nil, true)
		assert.False(t, claimed)
		assert.Nil(t, span)
		assert.False(t, keep)
		c.KeepMetadata(header)
		message := tracer.StartSpan(operationMessage)
		c.TagPendingMetadata(message)
		message.Finish()
		assert.Empty(t, tagsWithPrefix(requireFinished(t, mt), tagRequestMetadataPrefix))
	})

	t.Run("tags the call span once", func(t *testing.T) {
		startMockTracer(t)
		call := tracer.StartSpan(operationClient)
		c := newTestClientCall(call, enableMetadataTags)
		message := tracer.StartSpan(operationMessage)
		claimed, span, keep := c.ClaimMetadata(message, c.Begin())
		assert.True(t, claimed)
		assert.Same(t, call, span)
		assert.False(t, keep)
		claimed, span, keep = c.ClaimMetadata(message, c.Begin())
		assert.False(t, claimed)
		assert.Nil(t, span)
		assert.False(t, keep)
	})

	// The tracer may reuse a finished span for another trace, so the stream must not tag it even
	// though the span object is live again.
	t.Run("never tags a finished call span", func(t *testing.T) {
		mt := startMockTracer(t)
		reused := tracer.StartSpan("reused")
		c := newTestClientCall(reused, enableMetadataTags)
		c.finishClaimed.Store(true)
		traced := c.Begin()
		assert.False(t, traced)
		claimed, span, keep := c.ClaimMetadata(nil, traced)
		assert.True(t, claimed, "the operation still reads the metadata")
		assert.Nil(t, span)
		assert.False(t, keep)
		reused.Finish()
		assert.Empty(t, tagsWithPrefix(requireFinished(t, mt), tagRequestMetadataPrefix))
	})

	t.Run("not after the context ends the stream", func(t *testing.T) {
		mt := startMockTracer(t)
		ctx, cancel := context.WithCancel(context.Background())
		c := new(ClientCall)
		c.Init(ctx, newTestConfig(enableMetadataTags), tracer.StartSpan(operationClient), testCallFinish)
		cancel()
		require.Eventually(t, func() bool { return len(mt.FinishedSpans()) == 1 }, 5*time.Second, time.Millisecond)
		traced := c.Begin()
		claimed, span, keep := c.ClaimMetadata(nil, traced)
		c.End(nil, false, false, false)
		assert.True(t, claimed)
		assert.Nil(t, span)
		assert.False(t, keep)
		assert.True(t, c.finishClaimed.Load())
		assert.True(t, c.metadataRead.Load())
	})

	t.Run("without a call span, tags the message span", func(t *testing.T) {
		startMockTracer(t)
		c := newTestClientCall(nil, enableMetadataTags)
		message := tracer.StartSpan(operationMessage)
		claimed, span, keep := c.ClaimMetadata(message, c.Begin())
		assert.True(t, claimed)
		assert.Same(t, message, span)
		assert.False(t, keep)
	})

	t.Run("without a call or message span, keeps the metadata for the next message span", func(t *testing.T) {
		mt := startMockTracer(t)
		c := newTestClientCall(nil, enableMetadataTags)
		claimed, span, keep := c.ClaimMetadata(nil, c.Begin())
		require.True(t, claimed)
		assert.Nil(t, span)
		require.True(t, keep)
		c.KeepMetadata(header)
		// Another operation without a message span leaves it pending.
		claimed, _, keep = c.ClaimMetadata(nil, c.Begin())
		assert.False(t, claimed)
		assert.False(t, keep)
		c.TagPendingMetadata(nil)

		first, second, third := tracer.StartSpan("first"), tracer.StartSpan("second"), tracer.StartSpan("third")
		claimed, _, _ = c.ClaimMetadata(first, c.Begin())
		assert.False(t, claimed)
		c.TagPendingMetadata(second)
		claimed, _, _ = c.ClaimMetadata(third, c.Begin())
		assert.False(t, claimed)
		for _, span := range []*tracer.Span{first, second, third} {
			span.Finish()
		}
		spans := mt.FinishedSpans()
		require.Len(t, spans, 3)
		assert.Equal(t, wantTags, tagsWithPrefix(spans[0], tagRequestMetadataPrefix))
		for _, span := range spans[1:] {
			assert.Empty(t, tagsWithPrefix(span, tagRequestMetadataPrefix), span.OperationName())
		}
	})

	t.Run("concurrent operations claim the metadata once", func(t *testing.T) {
		startMockTracer(t)
		c := newTestClientCall(tracer.StartSpan(operationClient), enableMetadataTags)
		var claims atomic.Int32
		var wg sync.WaitGroup
		for range 8 {
			wg.Go(func() {
				if claimed, _, _ := c.ClaimMetadata(nil, c.Begin()); claimed {
					claims.Add(1)
				}
			})
		}
		wg.Wait()
		assert.Equal(t, int32(1), claims.Load())
	})
}

// TestClientCallAllocs checks that the bookkeeping of an operation that doesn't end the stream
// allocates nothing.
func TestClientCallAllocs(t *testing.T) {
	startMockTracer(t)
	for _, test := range []struct {
		name string
		span *tracer.Span
	}{
		{name: "call span", span: tracer.StartSpan(operationClient)},
		{name: "no call span"},
	} {
		t.Run(test.name, func(t *testing.T) {
			c := newTestClientCall(test.span, enableMetadataTags)
			c.ClaimMetadata(nil, true)
			assert.Zero(t, testing.AllocsPerRun(100, func() {
				traced := c.Begin()
				c.ClaimMetadata(nil, traced)
				c.TagPendingMetadata(nil)
				c.End(io.EOF, false, false, false)
			}))
		})
	}
}
