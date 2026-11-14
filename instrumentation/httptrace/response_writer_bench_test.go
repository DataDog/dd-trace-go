// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package httptrace

import (
	"bufio"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

type benchHijacker struct{}

func (benchHijacker) Hijack() (net.Conn, *bufio.ReadWriter, error) { return nil, nil, nil }

// BenchmarkWrapResponseWriter measures the cost of wrapping a writer that has
// none, one or both of Flusher and Hijacker.
func BenchmarkWrapResponseWriter(b *testing.B) {
	rec := httptest.NewRecorder()
	writers := map[string]http.ResponseWriter{
		"none": struct{ http.ResponseWriter }{rec},
		"flusher": struct {
			http.ResponseWriter
			http.Flusher
		}{rec, rec},
		"hijacker": struct {
			http.ResponseWriter
			http.Hijacker
		}{rec, benchHijacker{}},
		"both": struct {
			http.ResponseWriter
			http.Flusher
			http.Hijacker
		}{rec, rec, benchHijacker{}},
	}
	for _, name := range []string{"none", "flusher", "hijacker", "both"} {
		w := writers[name]
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				wrapResponseWriter(w)
			}
		})
	}
}

type benchOpaqueFlusher struct {
	http.ResponseWriter
}

func (benchOpaqueFlusher) Flush() {}

// BenchmarkFlush measures a wrap and one Flush. "known" is a Flush on a server
// writer type (the recorder is one), "opaque" is a Flush on another type.
func BenchmarkFlush(b *testing.B) {
	rec := httptest.NewRecorder()
	for name, w := range map[string]http.ResponseWriter{
		"known":  rec,
		"opaque": benchOpaqueFlusher{rec},
	} {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				tw, _ := wrapResponseWriter(w)
				tw.(http.Flusher).Flush()
			}
		})
	}
}

// BenchmarkFlushRepeated measures one Flush on a wrapped writer, many times on
// the same wrapper. A known writer is recorded at the first Flush, so the
// type check is not repeated. An opaque writer is never recorded, so the type
// check repeats on each Flush.
func BenchmarkFlushRepeated(b *testing.B) {
	rec := httptest.NewRecorder()
	for name, w := range map[string]http.ResponseWriter{
		"known":  rec,
		"opaque": benchOpaqueFlusher{rec},
	} {
		b.Run(name, func(b *testing.B) {
			tw, _ := wrapResponseWriter(w)
			f := tw.(http.Flusher)
			b.ReportAllocs()
			for b.Loop() {
				f.Flush()
			}
		})
	}
}
