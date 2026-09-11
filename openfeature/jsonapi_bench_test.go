// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// BenchmarkParseUFCEnvelope measures decoding one configuration update, the only
// new per-update CPU cost the Agentless source introduces. Flag evaluation is
// unaffected by the delivery source, so it is covered by the benchmarks in
// provider_bench_test.go instead. The canonical system-test fixture is used
// rather than a synthetic one so the number tracks a realistic payload.
func BenchmarkParseUFCEnvelope(b *testing.B) {
	body := benchmarkUFCEnvelope(b)

	b.Run("json", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(body)))
		for b.Loop() {
			config, err := parseUFCEnvelope(body)
			if err != nil {
				b.Fatal(err)
			}
			_ = config
		}
	})

	// The gzip subtest covers what a changed poll actually does: the source sets
	// Accept-Encoding itself, so a 200 arrives compressed and is decompressed
	// before parsing. An unchanged poll is a bodyless 304 and decodes nothing.
	b.Run("gzip", func(b *testing.B) {
		compressed := gzipBytes(b, body)

		b.ReportAllocs()
		b.SetBytes(int64(len(body)))
		for b.Loop() {
			// A new reader per iteration: readAgentlessResponseBody consumes it,
			// and that allocation is part of the cost being measured.
			resp := &http.Response{
				Header: http.Header{"Content-Encoding": []string{"gzip"}},
				Body:   io.NopCloser(bytes.NewReader(compressed)),
			}
			decoded, err := readAgentlessResponseBody(resp, maxResponseBodyBytes)
			if err != nil {
				b.Fatal(err)
			}
			config, err := parseUFCEnvelope(decoded)
			if err != nil {
				b.Fatal(err)
			}
			_ = config
		}
	})
}

// benchmarkUFCEnvelope returns the canonical fixture wrapped in the JSON:API
// envelope the Agentless endpoint serves.
func benchmarkUFCEnvelope(b *testing.B) []byte {
	b.Helper()
	attributes, err := os.ReadFile(filepath.Join("ffe-system-test-data", "ufc-config.json"))
	if err != nil {
		b.Fatalf("read canonical FFE fixtures: %v (initialize them with `git submodule update --init --recursive`)", err)
	}
	return wrapUFCEnvelope(b, ufcResourceType, json.RawMessage(attributes))
}

func gzipBytes(b *testing.B, data []byte) []byte {
	b.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	_, err := gz.Write(data)
	require.NoError(b, err)
	require.NoError(b, gz.Close())
	return buf.Bytes()
}
