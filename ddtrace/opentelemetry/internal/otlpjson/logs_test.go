// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package otlpjson_test

import (
	"context"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/plog/plogotlp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/trace"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/opentelemetry/internal/otlpjson"
)

// recordCollector is an sdklog.Exporter keeping every record it receives.
type recordCollector struct {
	mu      sync.Mutex
	records []sdklog.Record
}

func (c *recordCollector) Export(_ context.Context, rs []sdklog.Record) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, r := range rs {
		c.records = append(c.records, r.Clone())
	}
	return nil
}
func (c *recordCollector) Shutdown(context.Context) error   { return nil }
func (c *recordCollector) ForceFlush(context.Context) error { return nil }

// emitRecords runs emit against a logger provider built with res, and returns the SDK records it produced.
func emitRecords(t *testing.T, res *resource.Resource, emit func(newLogger func(name string, opts ...otellog.LoggerOption) otellog.Logger)) []sdklog.Record {
	t.Helper()
	c := &recordCollector{}
	provider := sdklog.NewLoggerProvider(sdklog.WithResource(res), sdklog.WithProcessor(sdklog.NewSimpleProcessor(c)))
	emit(func(name string, opts ...otellog.LoggerOption) otellog.Logger { return provider.Logger(name, opts...) })
	require.NoError(t, provider.Shutdown(context.Background()))
	return c.records
}

func sampleLogRecords(t *testing.T) []sdklog.Record {
	res := resource.NewWithAttributes("https://opentelemetry.io/schemas/1.26.0",
		attribute.String("service.name", "svc"), attribute.String("env", "test"))
	spanCtx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{0xde, 0xad, 0xbe, 0xef, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12},
		SpanID:     trace.SpanID{0xca, 0xfe, 1, 2, 3, 4, 5, 6},
		TraceFlags: trace.FlagsSampled,
	}))
	ts := time.Unix(1700000000, 123456789)
	return emitRecords(t, res, func(newLogger func(string, ...otellog.LoggerOption) otellog.Logger) {
		scoped := newLogger("scope-a",
			otellog.WithInstrumentationVersion("1.2.3"),
			otellog.WithSchemaURL("https://opentelemetry.io/schemas/1.27.0"),
			otellog.WithInstrumentationAttributes(attribute.String("scope.attr", "v")))
		plain := newLogger("scope-b")

		var full otellog.Record
		full.SetTimestamp(ts)
		full.SetObservedTimestamp(ts.Add(time.Second))
		full.SetSeverity(otellog.SeverityWarn2)
		full.SetSeverityText("WARN")
		full.SetEventName("my.event")
		full.SetBody(attribute.StringValue("hello \"world\"\n\x01 é 日本"))
		full.AddAttributes(
			attribute.String("s", "str"),
			attribute.Bool("b", true),
			attribute.Int64("i", math.MaxInt64),
			attribute.Float64("f", 1.5),
			attribute.Float64("nan", math.NaN()),
			attribute.Float64("inf", math.Inf(1)),
			attribute.StringSlice("ss", []string{"a", "b"}),
			attribute.Int64Slice("is", []int64{1, -2}),
			attribute.Float64Slice("fs", []float64{0.5}),
			attribute.BoolSlice("bs", []bool{true, false}),
			attribute.ByteSlice("bytes", []byte{0, 1, 2, 255}),
			attribute.Map("map", attribute.String("k", "v"), attribute.Slice("nested", attribute.Int64Value(7), attribute.StringValue("x"))),
		)
		scoped.Emit(spanCtx, full)

		var mapBody otellog.Record
		mapBody.SetSeverity(otellog.SeverityError)
		mapBody.SetBody(attribute.MapValue(attribute.String("msg", "boom"), attribute.Int64("code", 500)))
		scoped.Emit(context.Background(), mapBody)

		var bare otellog.Record
		plain.Emit(context.Background(), bare)

		var bytesBody otellog.Record
		bytesBody.SetTimestamp(ts)
		bytesBody.SetBody(attribute.ByteSliceValue([]byte("raw")))
		plain.Emit(spanCtx, bytesBody)
	})
}

func captureProtobufLogs(t *testing.T, records []sdklog.Record) plogotlp.ExportRequest {
	t.Helper()
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
	}))
	defer srv.Close()
	exp, err := otlploghttp.New(context.Background(), otlploghttp.WithEndpointURL(srv.URL+"/v1/logs"))
	require.NoError(t, err)
	require.NoError(t, exp.Export(context.Background(), records))
	require.NoError(t, exp.Shutdown(context.Background()))
	req := plogotlp.NewExportRequest()
	require.NoError(t, req.UnmarshalProto(body))
	return req
}

// TestEncodeLogsMatchesUpstreamProtobuf is the drift guard: the JSON we write must
// decode to the same data as the protobuf the upstream exporter writes for the
// same records. It fails when an OpenTelemetry upgrade adds a field we do not encode.
func TestEncodeLogsMatchesUpstreamProtobuf(t *testing.T) {
	records := sampleLogRecords(t)

	got := plogotlp.NewExportRequest()
	require.NoError(t, got.UnmarshalJSON(otlpjson.EncodeLogs(nil, records)))
	want := captureProtobufLogs(t, records)

	gotProto, err := got.MarshalProto()
	require.NoError(t, err)
	wantProto, err := want.MarshalProto()
	require.NoError(t, err)
	assert.Equal(t, wantProto, gotProto)
	assert.Equal(t, 4, got.Logs().LogRecordCount())
}

func TestEncodeLogsGroupsByResourceAndScope(t *testing.T) {
	a := emitRecords(t, resource.NewWithAttributes("", attribute.String("r", "a")), func(l func(string, ...otellog.LoggerOption) otellog.Logger) {
		for _, scope := range []string{"s1", "s2", "s1"} {
			var r otellog.Record
			r.SetBody(attribute.StringValue(scope))
			l(scope).Emit(context.Background(), r)
		}
	})
	b := emitRecords(t, resource.NewWithAttributes("", attribute.String("r", "b")), func(l func(string, ...otellog.LoggerOption) otellog.Logger) {
		var r otellog.Record
		l("s1").Emit(context.Background(), r)
	})

	req := plogotlp.NewExportRequest()
	require.NoError(t, req.UnmarshalJSON(otlpjson.EncodeLogs(nil, append(a, b...))))

	rls := req.Logs().ResourceLogs()
	require.Equal(t, 2, rls.Len())
	assert.Equal(t, 2, rls.At(0).ScopeLogs().Len(), "s1 and s2 under resource a")
	assert.Equal(t, 2, rls.At(0).ScopeLogs().At(0).LogRecords().Len(), "both s1 records are grouped")
	assert.Equal(t, 1, rls.At(1).ScopeLogs().Len())
}

func TestEncodeLogsFollowsOTLPJSONRules(t *testing.T) {
	records := emitRecords(t, resource.Empty(), func(l func(string, ...otellog.LoggerOption) otellog.Logger) {
		var r otellog.Record
		r.SetSeverity(otellog.SeverityInfo)
		r.SetBody(attribute.StringValue("x"))
		r.SetTimestamp(time.Unix(0, 42))
		r.SetObservedTimestamp(time.Unix(0, 43))
		r.AddAttributes(attribute.Int64("n", 5))
		ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
			TraceID: trace.TraceID{0xAB, 1}, SpanID: trace.SpanID{0xCD, 2},
		}))
		l("").Emit(ctx, r)
	})

	assert.JSONEq(t, `{"resourceLogs":[{"scopeLogs":[{"logRecords":[{
		"timeUnixNano":"42",
		"observedTimeUnixNano":"43",
		"severityNumber":9,
		"body":{"stringValue":"x"},
		"attributes":[{"key":"n","value":{"intValue":"5"}}],
		"traceId":"ab010000000000000000000000000000",
		"spanId":"cd02000000000000"
	}]}]}]}`, string(otlpjson.EncodeLogs(nil, records)))
}

func TestEncodeLogsReplacesInvalidUTF8(t *testing.T) {
	records := emitRecords(t, resource.Empty(), func(l func(string, ...otellog.LoggerOption) otellog.Logger) {
		var r otellog.Record
		r.SetBody(attribute.StringValue("bad\xffbyte"))
		l("").Emit(context.Background(), r)
	})
	req := plogotlp.NewExportRequest()
	require.NoError(t, req.UnmarshalJSON(otlpjson.EncodeLogs(nil, records)))
	body := req.Logs().ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0).Body().Str()
	assert.Equal(t, "bad�byte", body)
}

func BenchmarkEncodeLogs(b *testing.B) {
	records := make([]sdklog.Record, 512)
	for i := range records {
		records[i].SetBody(attribute.StringValue("benchmark log line"))
		records[i].SetSeverity(otellog.SeverityInfo)
		records[i].AddAttributes(attribute.String("k", "v"), attribute.Int("n", i))
	}
	var buf []byte

	b.ReportAllocs()
	for b.Loop() {
		buf = otlpjson.EncodeLogs(buf[:0], records)
	}
}
