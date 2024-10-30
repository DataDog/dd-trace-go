// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package opentelemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/httpmem"

	"github.com/stretchr/testify/assert"
	"github.com/tinylib/msgp/msgp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"
)

type traces [][]map[string]interface{}

func mockTracerProvider(t *testing.T, opts ...tracer.StartOption) (tp *TracerProvider, payloads chan traces, cleanup func()) {
	payloads = make(chan traces)
	s, c := httpmem.ServerAndClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v0.4/traces":
			if h := r.Header.Get("X-Datadog-Trace-Count"); h == "0" {
				return
			}
			req := r.Clone(context.Background())
			defer req.Body.Close()
			buf, err := io.ReadAll(req.Body)
			if err != nil || len(buf) == 0 {
				t.Fatalf("Test agent: Error receiving traces: %v", err)
			}
			var payload bytes.Buffer
			_, err = msgp.UnmarshalAsJSON(&payload, buf)
			if err != nil {
				t.Fatalf("Failed to unmarshal payload bytes as JSON: %v", err)
			}
			var tr [][]map[string]interface{}
			err = json.Unmarshal(payload.Bytes(), &tr)
			if err != nil || len(tr) == 0 {
				t.Fatalf("Failed to unmarshal payload bytes as trace: %v", err)
			}
			payloads <- tr
		default:
			if r.Method == "GET" {
				// Write an empty JSON object to the output, to avoid spurious decoding
				// errors to be reported in the logs, which may lead someone
				// investigating a test failure into the wrong direction.
				w.Write([]byte("{}"))
				return
			}
		}
		w.WriteHeader(200)
	}))
	opts = append(opts, tracer.WithHTTPClient(c))
	tp = NewTracerProvider(opts...)
	otel.SetTracerProvider(tp)
	return tp, payloads, func() {
		if err := s.Close(); err != nil {
			t.Fatalf("Test Agent server Close failure: %v", err)
		}
		if err := tp.Shutdown(); err != nil {
			t.Fatalf("Tracer Provider shutdown failure: %v", err)
		}
	}
}

func waitForPayload(payloads chan traces) (traces, error) {
	select {
	case p := <-payloads:
		return p, nil
	case <-time.After(10 * time.Second):
		return nil, fmt.Errorf("Timed out waiting for traces")
	}
}

func TestSpanResourceNameDefault(t *testing.T) {
	assert := assert.New(t)
	ctx := context.Background()

	_, payloads, cleanup := mockTracerProvider(t)
	tr := otel.Tracer("")
	defer cleanup()

	_, sp := tr.Start(ctx, "OperationName")
	sp.End()

	tracer.Flush()
	traces, err := waitForPayload(payloads)
	if err != nil {
		t.Fatalf(err.Error())
	}
	p := traces[0]
	assert.Equal("internal", p[0]["name"])
	assert.Equal("OperationName", p[0]["resource"])
}

func TestSpanSetName(t *testing.T) {
	assert := assert.New(t)

	_, payloads, cleanup := mockTracerProvider(t)
	tr := otel.Tracer("")
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, sp := tr.Start(ctx, "OldName")
	sp.SetName("NewName")
	sp.End()

	tracer.Flush()
	traces, err := waitForPayload(payloads)
	if err != nil {
		t.Fatalf(err.Error())
	}
	p := traces[0]
	assert.Equal(strings.ToLower("NewName"), p[0]["name"])
}

func TestSpanLink(t *testing.T) {
	assert := assert.New(t)
	_, payloads, cleanup := mockTracerProvider(t)
	tr := otel.Tracer("")
	defer cleanup()

	// Use traceID, spanID, and traceflags that can be unmarshalled from unint64 to float64 without loss of precision
	traceID, _ := oteltrace.TraceIDFromHex("00000000000001c8000000000000007b")
	spanID, _ := oteltrace.SpanIDFromHex("000000000000000f")
	traceStateVal := "dd_origin=ci"
	traceState, _ := oteltrace.ParseTraceState(traceStateVal)
	remoteSpanContext := oteltrace.NewSpanContext(
		oteltrace.SpanContextConfig{
			TraceID:    traceID,
			SpanID:     spanID,
			TraceFlags: oteltrace.FlagsSampled,
			TraceState: traceState,
			Remote:     true,
		},
	)

	// Create a span with a link to a remote span
	_, span := tr.Start(context.Background(), "span_with_link",
		oteltrace.WithLinks(oteltrace.Link{
			SpanContext: remoteSpanContext,
			Attributes:  []attribute.KeyValue{attribute.String("link.name", "alpha_transaction")},
		}))
	span.End()

	tracer.Flush()
	payload, err := waitForPayload(payloads)
	if err != nil {
		t.Fatalf(err.Error())
	}
	assert.NotNil(payload)
	assert.Len(payload, 1)    // only one trace
	assert.Len(payload[0], 1) // only one span

	var spanLinks []ddtrace.SpanLink
	spanLinkBytes, _ := json.Marshal(payload[0][0]["span_links"])
	json.Unmarshal(spanLinkBytes, &spanLinks)
	assert.Len(spanLinks, 1) // only one span link

	// Ensure the span link has the correct values
	assert.Equal(uint64(123), spanLinks[0].TraceID)
	assert.Equal(uint64(456), spanLinks[0].TraceIDHigh)
	assert.Equal(uint64(15), spanLinks[0].SpanID)
	assert.Equal(map[string]string{"link.name": "alpha_transaction"}, spanLinks[0].Attributes)
	assert.Equal(traceStateVal, spanLinks[0].Tracestate)
	assert.Equal(uint32(0x80000001), spanLinks[0].Flags) // sampled and set
}

func TestSpanEnd(t *testing.T) {
	assert := assert.New(t)
	_, payloads, cleanup := mockTracerProvider(t)
	tr := otel.Tracer("")
	defer cleanup()

	name, ignoredName := "trueName", "invalidName"
	code, ignoredCode := codes.Error, codes.Ok
	msg, ignoredMsg := "error_desc", "ok_desc"
	attributes := map[string]string{"trueKey": "trueVal"}
	ignoredAttributes := map[string]string{"trueKey": "fakeVal", "invalidKey": "invalidVal"}

	_, sp := tr.Start(context.Background(), name)
	sp.SetStatus(code, msg)
	for k, v := range attributes {
		sp.SetAttributes(attribute.String(k, v))
	}
	assert.True(sp.IsRecording())
	now := time.Now()
	nowUnixNano := now.UnixNano()
	sp.AddEvent("evt1", oteltrace.WithTimestamp(now))
	sp.AddEvent("evt2", oteltrace.WithTimestamp(now), oteltrace.WithAttributes(attribute.String("key1", "value"), attribute.Int("key2", 1234)))

	sp.End()
	assert.False(sp.IsRecording())

	// following operations should not be able to modify the Span since the span has finished
	sp.SetName(ignoredName)
	sp.SetStatus(ignoredCode, ignoredMsg)
	for k, v := range ignoredAttributes {
		sp.SetAttributes(attribute.String(k, v))
		sp.SetAttributes(attribute.String(k, v))
	}

	tracer.Flush()
	traces, err := waitForPayload(payloads)
	if err != nil {
		t.Fatalf(err.Error())
	}
	p := traces[0]

	assert.Equal(name, p[0]["resource"])
	assert.Equal(ext.SpanKindInternal, p[0]["name"]) // default
	assert.Equal(1.0, p[0]["error"])                 // this should be an error span
	meta := fmt.Sprintf("%v", p[0]["meta"])
	assert.Contains(meta, msg)
	for k, v := range attributes {
		assert.Contains(meta, fmt.Sprintf("%s:%s", k, v))
	}
	for k, v := range ignoredAttributes {
		assert.NotContains(meta, fmt.Sprintf("%s:%s", k, v))
	}
	jsonMeta := fmt.Sprintf(
		"events:[{\"name\":\"evt1\",\"time_unix_nano\":%v},{\"name\":\"evt2\",\"time_unix_nano\":%v,\"attributes\":{\"key1\":\"value\",\"key2\":1234}}]",
		nowUnixNano, nowUnixNano,
	)
	assert.Contains(meta, jsonMeta)
}

// This test verifies that setting the status of a span
// behaves accordingly to the Otel API spec
// (https://opentelemetry.io/docs/reference/specification/trace/api/#set-status)
// by checking the following:
//  1. attempts to set the value of `Unset` are ignored
//  2. description must only be used with `Error` value
//  3. setting the status to `Ok` is final and will override any
//     any prior or future status values
func TestSpanSetStatus(t *testing.T) {
	assert := assert.New(t)
	testData := []struct {
		code        codes.Code
		msg         string
		ignoredCode codes.Code
		ignoredMsg  string
	}{
		{
			code:        codes.Ok,
			msg:         "ok_description",
			ignoredCode: codes.Error,
			ignoredMsg:  "error_description",
		},
		{
			code:        codes.Error,
			msg:         "error_description",
			ignoredCode: codes.Unset,
			ignoredMsg:  "unset_description",
		},
	}
	_, payloads, cleanup := mockTracerProvider(t)
	tr := otel.Tracer("")
	defer cleanup()

	for _, test := range testData {
		t.Run(fmt.Sprintf("Setting Code: %d", test.code), func(t *testing.T) {
			var sp oteltrace.Span
			testStatus := func() {
				sp.End()
				tracer.Flush()
				traces, err := waitForPayload(payloads)
				if err != nil {
					t.Fatalf(err.Error())
				}
				p := traces[0]
				// An error description is set IFF the span has an error
				// status code value. Messages related to any other status code
				// are ignored.
				meta := fmt.Sprintf("%v", p[0]["meta"])
				if test.code == codes.Error {
					assert.Contains(meta, test.msg)
				} else {
					assert.NotContains(meta, test.msg)
				}
				assert.NotContains(meta, test.ignoredCode)
			}
			_, sp = tr.Start(context.Background(), "test")
			sp.SetStatus(test.code, test.msg)
			sp.SetStatus(test.ignoredCode, test.ignoredMsg)
			testStatus()

			_, sp = tr.Start(context.Background(), "test")
			sp.SetStatus(test.code, test.msg)
			sp.SetStatus(test.ignoredCode, test.ignoredMsg)
			testStatus()
		})
	}
}

func TestSpanAddEvent(t *testing.T) {
	assert := assert.New(t)
	_, _, cleanup := mockTracerProvider(t)
	tr := otel.Tracer("")
	defer cleanup()

	t.Run("event with attributes", func(t *testing.T) {
		_, sp := tr.Start(context.Background(), "span_event")
		// When no timestamp option is provided, otel will generate a timestamp for the event
		// We can't know the exact time that the event is added, but we can create start and end "bounds" and assert
		// that the event's eventual timestamp is between those bounds
		timeStartBound := time.Now().UnixNano()
		sp.AddEvent("My event!", oteltrace.WithAttributes(
			attribute.Int("pid", 4328),
			attribute.String("signal", "SIGHUP"),
			// two attributes with same key, last-set attribute takes precedence
			attribute.Bool("condition", true),
			attribute.Bool("condition", false),
		))
		timeEndBound := time.Now().UnixNano()
		sp.End()
		dd := sp.(*span)

		// Assert event exists under span events
		assert.Len(dd.events, 1)
		e := dd.events[0]
		assert.Equal(e.Name, "My event!")
		// assert event timestamp is [around] the expected time
		assert.True((e.TimeUnixNano) >= timeStartBound && e.TimeUnixNano <= timeEndBound)
		// Assert both attributes exist on the event
		assert.Len(e.Attributes, 3)
		// Assert attribute key-value fields
		// note that attribute.Int("pid", 4328) created an attribute with value int64(4328), hence why the `want` is in int64 format
		wantAttrs := map[string]interface{}{
			"pid":       int64(4328),
			"signal":    "SIGHUP",
			"condition": false,
		}
		for k, v := range wantAttrs {
			assert.True(attributesContains(e.Attributes, k, v))
		}
	})
	t.Run("event with timestamp", func(t *testing.T) {
		_, sp := tr.Start(context.Background(), "span_event")
		// generate micro and nano second timestamps
		now := time.Now()
		timeMicro := now.UnixMicro()
		// pass microsecond timestamp into timestamp option
		sp.AddEvent("My event!", oteltrace.WithTimestamp(time.UnixMicro(timeMicro)))
		sp.End()

		dd := sp.(*span)
		assert.Len(dd.events, 1)
		e := dd.events[0]
		// assert resulting timestamp is in nanoseconds
		assert.Equal(timeMicro*1000, e.TimeUnixNano)
	})
	t.Run("mulitple events", func(t *testing.T) {
		_, sp := tr.Start(context.Background(), "sp")
		now := time.Now()
		sp.AddEvent("evt1", oteltrace.WithTimestamp(now))
		sp.AddEvent("evt2", oteltrace.WithTimestamp(now))
		sp.End()
		dd := sp.(*span)
		assert.Len(dd.events, 2)
	})
}

// attributesContains returns true if attrs contains an attribute.KeyValue with the provided key and val
func attributesContains(attrs map[string]interface{}, key string, val interface{}) bool {
	for k, v := range attrs {
		if k == key && v == val {
			return true
		}
	}
	return false
}

func TestSpanContextWithStartOptions(t *testing.T) {
	assert := assert.New(t)
	_, payloads, cleanup := mockTracerProvider(t)
	tr := otel.Tracer("")
	defer cleanup()

	startTime := time.Now()
	duration := time.Second * 5
	spanID := uint64(1234567890)
	ctx, sp := tr.Start(
		ContextWithStartOptions(context.Background(),
			tracer.ResourceName("persisted_ctx_rsc"),
			tracer.ServiceName("persisted_srv"),
			tracer.StartTime(startTime),
			tracer.WithSpanID(spanID),
		), "op_name",
		oteltrace.WithAttributes(
			attribute.String(ext.ResourceName, ""),
			attribute.String(ext.ServiceName, "discarded")),
		oteltrace.WithSpanKind(oteltrace.SpanKindProducer),
	)

	_, child := tr.Start(ctx, "child")
	ddChild := child.(*span)
	// this verifies that options passed to the parent, such as tracer.WithSpanID(spanID)
	// weren't passed down to the child
	assert.NotEqual(spanID, ddChild.DD.Context().SpanID())
	child.End()

	EndOptions(sp, tracer.FinishTime(startTime.Add(duration)))
	sp.End()

	tracer.Flush()
	traces, err := waitForPayload(payloads)
	if err != nil {
		t.Fatalf(err.Error())
	}
	p := traces[0]
	t.Logf("%v", p[0])
	assert.Len(p, 2)
	assert.Equal("persisted_srv", p[0]["service"])
	assert.Equal("persisted_ctx_rsc", p[0]["resource"])
	assert.Equal(1234567890.0, p[0]["span_id"])
	assert.Equal("producer", p[0]["name"])
	meta := fmt.Sprintf("%v", p[0]["meta"])
	assert.Contains(meta, "producer")
	assert.Equal(float64(startTime.UnixNano()), p[0]["start"])
	assert.Equal(float64(duration.Nanoseconds()), p[0]["duration"])
	assert.NotContains(p, "discarded")
	assert.NotEqual(1234567890.0, p[1]["span_id"])
}

func TestSpanContextWithStartOptionsPriorityOrder(t *testing.T) {
	assert := assert.New(t)

	_, payloads, cleanup := mockTracerProvider(t)
	tr := otel.Tracer("")
	defer cleanup()

	startTime := time.Now()
	_, sp := tr.Start(
		ContextWithStartOptions(context.Background(),
			tracer.ResourceName("persisted_ctx_rsc"),
			tracer.ServiceName("persisted_srv"),
		), "op_name",
		oteltrace.WithTimestamp(startTime.Add(time.Second)),
		oteltrace.WithAttributes(attribute.String(ext.ServiceName, "discarded")),
		oteltrace.WithSpanKind(oteltrace.SpanKindProducer))
	sp.End()

	tracer.Flush()
	traces, err := waitForPayload(payloads)
	if err != nil {
		t.Fatalf(err.Error())
	}
	p := traces[0]
	assert.Equal("persisted_srv", p[0]["service"])
	assert.Equal("persisted_ctx_rsc", p[0]["resource"])
	meta := fmt.Sprintf("%v", p[0]["meta"])
	assert.Contains(meta, "producer")
	assert.NotContains(p, "discarded")
}

func TestSpanEndOptionsPriorityOrder(t *testing.T) {
	assert := assert.New(t)

	_, payloads, cleanup := mockTracerProvider(t)
	tr := otel.Tracer("")
	defer cleanup()

	startTime := time.Now()
	_, sp := tr.Start(
		ContextWithStartOptions(context.Background(),
			tracer.ResourceName("ctx_rsc"),
			tracer.ServiceName("ctx_srv"),
			tracer.StartTime(startTime),
			tracer.WithSpanID(1234567890),
		), "op_name")

	EndOptions(sp, tracer.FinishTime(startTime.Add(time.Second)))
	// Next Calls to EndOptions do not keep previous options
	EndOptions(sp, tracer.FinishTime(startTime.Add(time.Second*5)))
	// EndOptions timestamp should prevail
	sp.End(oteltrace.WithTimestamp(startTime.Add(time.Second * 3)))
	duration := time.Second * 5
	// making sure end options don't have effect after the span has returned
	EndOptions(sp, tracer.FinishTime(startTime.Add(duration)))
	sp.End()

	tracer.Flush()
	traces, err := waitForPayload(payloads)
	if err != nil {
		t.Fatalf(err.Error())
	}
	p := traces[0]
	assert.Equal(float64(duration.Nanoseconds()), p[0]["duration"])
}

func TestSpanEndOptions(t *testing.T) {
	assert := assert.New(t)

	_, payloads, cleanup := mockTracerProvider(t)
	tr := otel.Tracer("")
	defer cleanup()

	spanID := uint64(1234567890)
	startTime := time.Now()
	duration := time.Second * 5
	_, sp := tr.Start(
		ContextWithStartOptions(context.Background(),
			tracer.ResourceName("ctx_rsc"),
			tracer.ServiceName("ctx_srv"),
			tracer.StartTime(startTime),
			tracer.WithSpanID(spanID),
		), "op_name")
	EndOptions(sp, tracer.FinishTime(startTime.Add(duration)),
		tracer.WithError(errors.New("persisted_option")))
	sp.End()
	tracer.Flush()
	traces, err := waitForPayload(payloads)
	if err != nil {
		t.Fatalf(err.Error())
	}
	p := traces[0]
	assert.Equal("ctx_srv", p[0]["service"])
	assert.Equal("ctx_rsc", p[0]["resource"])
	assert.Equal(1234567890.0, p[0]["span_id"])
	assert.Equal(float64(startTime.UnixNano()), p[0]["start"])
	assert.Equal(float64(duration.Nanoseconds()), p[0]["duration"])
	meta := fmt.Sprintf("%v", p[0]["meta"])
	assert.Contains(meta, "persisted_option")
	assert.Equal(1.0, p[0]["error"]) // this should be an error span
}

func TestSpanSetAttributes(t *testing.T) {
	assert := assert.New(t)

	_, payloads, cleanup := mockTracerProvider(t)
	tr := otel.Tracer("")
	defer cleanup()

	toBeIgnored := map[string]string{"k1": "v1_old"}
	attributes := map[string]string{
		"k2": "v2",
		"k1": "v1_new",
		// maps to 'name'
		"operation.name": "ops",
		// maps to 'service'
		"service.name": "srv",
		// maps to 'resource'
		"resource.name": "rsr",
		// maps to 'type'
		"span.type": "db",
	}

	_, sp := tr.Start(context.Background(), "test")
	for k, v := range toBeIgnored {
		sp.SetAttributes(attribute.String(k, v))
	}
	for k, v := range attributes {
		sp.SetAttributes(attribute.String(k, v))
	}
	// maps to '_dd1.sr.eausr'
	sp.SetAttributes(attribute.Int("analytics.event", 1))

	sp.End()
	tracer.Flush()
	traces, err := waitForPayload(payloads)
	if err != nil {
		t.Fatalf(err.Error())
	}
	p := traces[0]
	meta := fmt.Sprintf("%v", p[0]["meta"])
	for k, v := range toBeIgnored {
		assert.NotContains(meta, fmt.Sprintf("%s:%s", k, v))
	}
	assert.Contains(meta, fmt.Sprintf("%s:%s", "k1", "v1_new"))
	assert.Contains(meta, fmt.Sprintf("%s:%s", "k2", "v2"))

	// reserved attributes
	assert.NotContains(meta, fmt.Sprintf("%s:%s", "name", "ops"))
	assert.NotContains(meta, fmt.Sprintf("%s:%s", "service", "srv"))
	assert.NotContains(meta, fmt.Sprintf("%s:%s", "resource", "rsr"))
	assert.NotContains(meta, fmt.Sprintf("%s:%s", "type", "db"))
	assert.Equal("ops", p[0]["name"])
	assert.Equal("srv", p[0]["service"])
	assert.Equal("rsr", p[0]["resource"])
	assert.Equal("db", p[0]["type"])
	metrics := fmt.Sprintf("%v", p[0]["metrics"])
	assert.Contains(metrics, fmt.Sprintf("%s:%s", "_dd1.sr.eausr", "1"))
}

func TestSpanSetAttributesWithRemapping(t *testing.T) {
	assert := assert.New(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, payloads, cleanup := mockTracerProvider(t)
	tr := otel.Tracer("")
	defer cleanup()

	_, sp := tr.Start(ctx, "custom")
	sp.SetAttributes(attribute.String("graphql.operation.type", "subscription"))

	sp.End()

	tracer.Flush()
	traces, err := waitForPayload(payloads)
	if err != nil {
		t.Fatalf(err.Error())
	}
	p := traces[0]
	assert.Equal("graphql.server.request", p[0]["name"])
}

func TestTracerStartOptions(t *testing.T) {
	assert := assert.New(t)

	_, payloads, cleanup := mockTracerProvider(t, tracer.WithEnv("test_env"), tracer.WithService("test_serv"))
	tr := otel.Tracer("")
	defer cleanup()

	_, sp := tr.Start(context.Background(), "test")
	sp.End()
	tracer.Flush()
	traces, err := waitForPayload(payloads)
	if err != nil {
		t.Fatalf(err.Error())
	}
	p := traces[0]
	assert.Equal("test_serv", p[0]["service"])
	meta := fmt.Sprintf("%v", p[0]["meta"])
	assert.Contains(meta, fmt.Sprintf("%s:%s", "env", "test_env"))
}

func TestOperationNameRemapping(t *testing.T) {
	assert := assert.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, payloads, cleanup := mockTracerProvider(t)
	tr := otel.Tracer("")
	defer cleanup()

	_, sp := tr.Start(ctx, "operation", oteltrace.WithAttributes(attribute.String("graphql.operation.type", "subscription")))
	sp.End()

	tracer.Flush()
	traces, err := waitForPayload(payloads)
	if err != nil {
		t.Fatalf(err.Error())
	}
	p := traces[0]
	assert.Equal("graphql.server.request", p[0]["name"])
}
func TestRemapName(t *testing.T) {
	assert := assert.New(t)
	testCases := []struct {
		spanKind oteltrace.SpanKind
		in       []attribute.KeyValue
		out      string
	}{
		{
			in:  []attribute.KeyValue{attribute.String("operation.name", "Ops")},
			out: "ops",
		},
		{
			in:  []attribute.KeyValue{},
			out: "internal",
		},
		{
			in:       []attribute.KeyValue{attribute.String("http.request.method", "POST")},
			spanKind: oteltrace.SpanKindClient,
			out:      "http.client.request",
		},
		{
			in:       []attribute.KeyValue{attribute.String("http.request.method", "POST")},
			spanKind: oteltrace.SpanKindServer,
			out:      "http.server.request",
		},
		{
			in:       []attribute.KeyValue{attribute.String("db.system", "Redis")},
			spanKind: oteltrace.SpanKindClient,
			out:      "redis.query",
		},
		{
			in:       []attribute.KeyValue{attribute.String("messaging.system", "kafka"), attribute.String("messaging.operation", "receive")},
			spanKind: oteltrace.SpanKindProducer,
			out:      "kafka.receive",
		},
		{
			in:       []attribute.KeyValue{attribute.String("messaging.system", "kafka"), attribute.String("messaging.operation", "receive")},
			spanKind: oteltrace.SpanKindConsumer,
			out:      "kafka.receive",
		},
		{
			in:       []attribute.KeyValue{attribute.String("messaging.system", "kafka"), attribute.String("messaging.operation", "receive")},
			spanKind: oteltrace.SpanKindClient,
			out:      "kafka.receive",
		},
		{
			in:       []attribute.KeyValue{attribute.String("rpc.system", "aws-api"), attribute.String("rpc.service", "Example_Method")},
			spanKind: oteltrace.SpanKindClient,
			out:      "aws." + "example_method" + ".request",
		},
		{
			in:       []attribute.KeyValue{attribute.String("rpc.system", "aws-api"), attribute.String("rpc.service", "")},
			spanKind: oteltrace.SpanKindClient,
			out:      "aws.client.request",
		},
		{
			in:       []attribute.KeyValue{attribute.String("rpc.system", "myservice.EchoService")},
			spanKind: oteltrace.SpanKindClient,
			out:      "myservice.echoservice.client.request",
		},
		{
			in:       []attribute.KeyValue{attribute.String("rpc.system", "myservice.EchoService")},
			spanKind: oteltrace.SpanKindServer,
			out:      "myservice.echoservice.server.request",
		},
		{
			in:       []attribute.KeyValue{attribute.String("faas.invoked_provider", "some_provIDER"), attribute.String("faas.invoked_name", "some_NAME")},
			spanKind: oteltrace.SpanKindClient,
			out:      "some_provider.some_name.invoke",
		},
		{
			in:       []attribute.KeyValue{attribute.String("faas.trigger", "some_NAME")},
			spanKind: oteltrace.SpanKindServer,
			out:      "some_name.invoke",
		},
		{
			in:  []attribute.KeyValue{attribute.String("graphql.operation.type", "subscription")},
			out: "graphql.server.request",
		},
		{
			in:       []attribute.KeyValue{attribute.String("network.protocol.name", "amqp")},
			spanKind: oteltrace.SpanKindServer,
			out:      "amqp.server.request",
		},
		{
			in:       []attribute.KeyValue{attribute.String("network.protocol.name", "")},
			spanKind: oteltrace.SpanKindServer,
			out:      "server.request",
		},
		{
			in:       []attribute.KeyValue{attribute.String("network.protocol.name", "amqp")},
			spanKind: oteltrace.SpanKindClient,
			out:      "amqp.client.request",
		},
		{
			in:       []attribute.KeyValue{attribute.String("network.protocol.name", "")},
			spanKind: oteltrace.SpanKindClient,
			out:      "client.request",
		},
		{
			in:       []attribute.KeyValue{attribute.String("messaging.system", "kafka"), attribute.String("messaging.operation", "receive")},
			spanKind: oteltrace.SpanKindServer,
			out:      "kafka.receive",
		},
		{
			in:  []attribute.KeyValue{attribute.Int("operation.name", 2)},
			out: "internal",
		},
	}
	_, payloads, cleanup := mockTracerProvider(t, tracer.WithEnv("test_env"), tracer.WithService("test_serv"))
	tr := otel.Tracer("")
	defer cleanup()

	for _, test := range testCases {
		t.Run("", func(t *testing.T) {
			_, sp := tr.Start(context.Background(), "some_name",
				oteltrace.WithSpanKind(test.spanKind), oteltrace.WithAttributes(test.in...))
			sp.End()

			tracer.Flush()
			traces, err := waitForPayload(payloads)
			if err != nil {
				t.Fatalf(err.Error())
			}
			p := traces[0]
			assert.Equal(test.out, p[0]["name"])
		})
	}
}

func TestRemapWithMultipleSetAttributes(t *testing.T) {
	assert := assert.New(t)

	_, payloads, cleanup := mockTracerProvider(t, tracer.WithEnv("test_env"), tracer.WithService("test_serv"))
	tr := otel.Tracer("")
	defer cleanup()

	_, sp := tr.Start(context.Background(), "otel_span_name",
		oteltrace.WithSpanKind(oteltrace.SpanKindServer))

	sp.SetAttributes(attribute.String("http.request.method", "GET"))
	sp.SetAttributes(attribute.String("resource.name", "new.name"))
	sp.SetAttributes(attribute.String("operation.name", "Overriden.name"))
	sp.SetAttributes(attribute.String("service.name", "new.service.name"))
	sp.SetAttributes(attribute.String("span.type", "new.span.type"))
	sp.SetAttributes(attribute.String("analytics.event", "true"))
	sp.SetAttributes(attribute.Int("http.response.status_code", 200))
	sp.End()

	tracer.Flush()
	traces, err := waitForPayload(payloads)
	if err != nil {
		t.Fatalf(err.Error())
	}
	p := traces[0]
	assert.Equal("overriden.name", p[0]["name"])
	assert.Equal("new.name", p[0]["resource"])
	assert.Equal("new.service.name", p[0]["service"])
	assert.Equal("new.span.type", p[0]["type"])
	metrics := fmt.Sprintf("%v", p[0]["metrics"])
	assert.Contains(metrics, fmt.Sprintf("%s:%s", "_dd1.sr.eausr", "1"))
	meta := fmt.Sprintf("%v", p[0]["meta"])
	assert.Contains(meta, fmt.Sprintf("%s:%s", "http.status_code", "200"))
}
