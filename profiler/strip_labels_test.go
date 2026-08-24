// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License, Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package profiler

import (
	"bytes"
	"io"
	"os"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/traceprof"
	"github.com/DataDog/dd-trace-go/v2/profiler/internal/pproflite"

	pprofile "github.com/google/pprof/profile"
	"github.com/stretchr/testify/require"
)

func BenchmarkStripTracerLabels(b *testing.B) {
	path := os.Getenv("STRIP_TRACER_LABELS_PROFILE")
	if path == "" {
		b.Skip("set STRIP_TRACER_LABELS_PROFILE to a raw CPU profile path")
	}
	profile, err := os.ReadFile(path)
	if err != nil {
		b.Fatal(err)
	}

	b.Logf("input size: %d bytes", len(profile))
	buf := new(bytes.Buffer)
	stripPPROFLabels(profile, buf)
	b.Logf("output size: %d bytes", buf.Len())

	b.SetBytes(int64(len(profile)))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if err := stripPPROFLabels(profile, io.Discard); err != nil {
			b.Fatal(err)
		}
	}
}

func TestStripTracerLabels(t *testing.T) {
	// Include a value used outside a tracer label to verify that only unreferenced
	// string table entries are removed.
	strings := []string{"", "samples", "count", traceprof.SpanID, "123", traceprof.TraceID, "456", traceprof.TraceEndpoint, "/users", "custom", "kept"}
	var input bytes.Buffer
	encoder := pproflite.NewEncoder(&input)
	for _, value := range strings {
		require.NoError(t, encoder.Encode(&pproflite.StringTable{Value: []byte(value)}))
	}
	require.NoError(t, encoder.Encode(&pproflite.SampleType{ValueType: pproflite.ValueType{Type: 1, Unit: 2}}))
	require.NoError(t, encoder.Encode(&pproflite.Sample{Value: []int64{1}, Label: []pproflite.Label{
		{Key: 3, Str: 4},
		{Key: 5, Str: 6},
		{Key: 7, Str: 8},
		{Key: 9, Str: 10},
	}}))

	var output bytes.Buffer
	require.NoError(t, stripPPROFLabels(input.Bytes(), &output))

	// Make sure we produced a valid profile.
	_, err := pprofile.ParseData(output.Bytes())
	require.NoError(t, err)

	var gotLabels []pproflite.Label
	var gotStrings []string
	decoder := pproflite.NewDecoder(output.Bytes())
	require.NoError(t, decoder.FieldEach(func(field pproflite.Field) error {
		switch field := field.(type) {
		case *pproflite.Sample:
			gotLabels = append(gotLabels, field.Label...)
		case *pproflite.StringTable:
			gotStrings = append(gotStrings, string(field.Value))
		}
		return nil
	}))

	require.Len(t, gotLabels, 3)
	require.Equal(t, traceprof.SpanID, gotStrings[gotLabels[0].Key])
	require.Equal(t, "123", gotStrings[gotLabels[0].Str])
	require.Equal(t, traceprof.TraceEndpoint, gotStrings[gotLabels[1].Key])
	require.Equal(t, "/users", gotStrings[gotLabels[1].Str])
	require.Equal(t, "custom", gotStrings[gotLabels[2].Key])
	require.Equal(t, "kept", gotStrings[gotLabels[2].Str])
	require.NotContains(t, gotStrings, traceprof.TraceID)
	require.NotContains(t, gotStrings, "456")
}
