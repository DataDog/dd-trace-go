// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"fmt"
	"io/fs"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// panicValue is a panic value whose own Error panics, so fmt cannot render
// it and re-panics. It panics explicitly rather than through a nil
// dereference: a signal panic in this nested position hits a runtime
// traceback failure on darwin/arm64 with Go 1.27.
type panicValue struct{}

func (panicValue) Error() string { panic("panicValue.Error") }

// formatPanicError is a fmt.Formatter whose Format panics with panicValue.
type formatPanicError struct{}

func (formatPanicError) Error() string          { return "format panic" }
func (formatPanicError) Format(fmt.State, rune) { panic(panicValue{}) }

// TestSpanErrorPanicPlaceholders checks every known panicking error from
// https://github.com/DataDog/dd-trace-go/issues/5812 through every entry
// point that records an error: no panic, the span lock is released, and a
// placeholder naming the error type is recorded.
func TestSpanErrorPanicPlaceholders(t *testing.T) {
	tr, _, _, stop, err := startTestTracer(t)
	require.NoError(t, err)
	defer stop()

	errs := []struct {
		name      string
		err       func() error
		key, want string
	}{
		{
			name: "typed-nil pointer",
			err:  func() error { var e *nilPtrError; return e },
			key:  ext.ErrorMsg,
			want: "<error message unavailable: *tracer.nilPtrError.Error panicked>",
		},
		{
			name: "fs.PathError with nil Err",
			err:  func() error { return &fs.PathError{Op: "open", Path: "/x"} },
			key:  ext.ErrorMsg,
			want: "<error message unavailable: *fs.PathError.Error panicked>",
		},
		{
			name: "Formatter whose panic value panics",
			err:  func() error { return formatPanicError{} },
			key:  ext.ErrorStack,
			want: "<error details unavailable: tracer.formatPanicError formatting panicked>",
		},
	}
	entries := map[string]func(error) *Span{
		"Finish": func(e error) *Span {
			s := tr.StartSpan("op")
			s.Finish(WithError(e))
			return s
		},
		"StartSpan Tag": func(e error) *Span { return tr.StartSpan("op", Tag(ext.Error, e)) },
		"setTags": func(e error) *Span {
			s := tr.StartSpan("op")
			s.setTags(map[string]any{ext.Error: e})
			return s
		},
		"SetTag": func(e error) *Span {
			s := tr.StartSpan("op")
			s.SetTag(ext.Error, e)
			return s
		},
	}
	for _, tc := range errs {
		for entry, run := range entries {
			t.Run(tc.name+"/"+entry, func(t *testing.T) {
				var s *Span
				require.NotPanics(t, func() { s = run(tc.err()) })
				require.NotNil(t, s)
				assertSpanUnlocked(t, s)

				got, _ := getMeta(s, tc.key)
				assert.Equal(t, tc.want, got)
				typ, _ := getMeta(s, ext.ErrorType)
				assert.NotEmpty(t, typ)
				assert.Equal(t, int32(1), s.error)
			})
		}
	}
}
