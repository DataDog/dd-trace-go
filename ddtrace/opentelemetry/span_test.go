// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package opentelemetry

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/tinylib/msgp/msgp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/httpmem"
)

func mockTracerProvider(t *testing.T, opts ...tracer.StartOption) (tp *TracerProvider, payloads chan string, cleanup func()) {
	payloads = make(chan string)
	s, c := httpmem.ServerAndClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v0.4/traces":
			if h := r.Header.Get("X-Datadog-Trace-Count"); h == "0" {
				return
			}
			buf, err := io.ReadAll(r.Body)
			if err != nil || len(buf) == 0 {
				t.Fatalf("Test agent: Error receiving traces")
			}
			var js bytes.Buffer
			msgp.UnmarshalAsJSON(&js, buf)
			payloads <- js.String()
		}
		w.WriteHeader(200)
	}))
	opts = append(opts, tracer.WithHTTPClient(c))
	tp = NewTracerProvider(opts...)
	otel.SetTracerProvider(tp)
	return tp, payloads, func() {
		tp.Shutdown()
		s.Close()
	}
}

func waitForPayload(ctx context.Context, payloads chan string) (string, error) {
	select {
	case <-ctx.Done():
		return "", fmt.Errorf("Timed out waiting for traces")
	case p := <-payloads:
		return p, nil
	}
	return "", nil
}

func TestSpanSetName(t *testing.T) {
	assert := assert.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_, payloads, cleanup := mockTracerProvider(t)
	tr := otel.Tracer("")
	defer cleanup()

	_, sp := tr.Start(ctx, "OldName")
	sp.SetName("NewName")
	sp.End()

	tracer.Flush()
	p, err := waitForPayload(ctx, payloads)
	if err != nil {
		t.Fatalf(err.Error())
	}
	assert.Contains(p, "NewName")
}

func TestSpanEnd(t *testing.T) {
	assert := assert.New(t)
	testData := struct {
		trueName        string
		falseName       string
		trueError       codes.Code
		trueErrorMsg    string
		falseError      codes.Code
		falseErrorMsg   string
		trueAttributes  map[string]string
		falseAttributes map[string]string
	}{
		trueName:        "trueName",
		falseName:       "invalidName",
		trueError:       codes.Error,
		trueErrorMsg:    "error_description",
		falseError:      codes.Ok,
		falseErrorMsg:   "ok_description",
		trueAttributes:  map[string]string{"trueKey": "trueVal"},
		falseAttributes: map[string]string{"trueKey": "fakeVal", "invalidKey": "invalidVal"},
	}
	_, payloads, cleanup := mockTracerProvider(t)
	tr := otel.Tracer("")
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, sp := tr.Start(context.Background(), testData.trueName)
	sp.SetStatus(codes.Error, testData.trueErrorMsg)
	for k, v := range testData.trueAttributes {
		sp.SetAttributes(attribute.String(k, v))
	}
	assert.True(sp.IsRecording())
	sp.End()
	assert.False(sp.IsRecording())
	// following operations should not be able to modify the Span since the span has finished
	sp.SetName(testData.trueName)
	sp.SetStatus(testData.falseError, testData.falseErrorMsg)
	for k, v := range testData.trueAttributes {
		sp.SetAttributes(attribute.String(k, v))
		sp.SetAttributes(attribute.String(k, v))
	}

	tracer.Flush()
	payload, err := waitForPayload(ctx, payloads)
	if err != nil {
		t.Fatalf(err.Error())
	}
	assert.Contains(payload, testData.trueErrorMsg)
	assert.NotContains(payload, testData.falseErrorMsg)
	assert.Contains(payload, testData.trueName)
	assert.NotContains(payload, testData.falseName)
	for k, v := range testData.trueAttributes {
		assert.Contains(payload, fmt.Sprintf("\"%s\":\"%s\"", k, v))
	}
	for k, v := range testData.falseAttributes {
		assert.NotContains(payload, fmt.Sprintf("\"%s\":\"%s\"", k, v))
	}

}

// This test verifies that setting the status of a span
// behaves accordingly to the Otel API spec
// (https://opentelemetry.io/docs/reference/specification/trace/api/#set-status)
// By checking the following:
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
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()

			var sp oteltrace.Span
			testStatus := func() {
				sp.End()
				tracer.Flush()
				payload, err := waitForPayload(ctx, payloads)
				if err != nil {
					t.Fatalf(err.Error())
				}
				// An error description is set IFF the span has an error
				// status code value. Messages related to any other status code
				// are ignored.
				if test.code == codes.Error {
					assert.Contains(payload, test.msg)
				} else {
					assert.NotContains(payload, test.msg)
				}
				assert.NotContains(payload, test.ignoredCode)
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

func TestSpanSetAttributes(t *testing.T) {
	assert := assert.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_, payloads, cleanup := mockTracerProvider(t)
	tr := otel.Tracer("")
	defer cleanup()

	attributes := [][]string{{"k1", "v1_old"},
		{"k2", "v2"},
		{"k1", "v1_new"}}

	_, sp := tr.Start(context.Background(), "test")
	for _, tag := range attributes {
		sp.SetAttributes(attribute.String(tag[0], tag[1]))
	}
	sp.End()
	tracer.Flush()
	payload, err := waitForPayload(ctx, payloads)
	if err != nil {
		t.Fatalf(err.Error())
	}
	assert.Contains(payload, "k1")
	assert.Contains(payload, "k2")
	assert.Contains(payload, "v1_new")
	assert.Contains(payload, "v2")
	assert.NotContains(payload, "v1_old")
}

func TestTracerStartOptions(t *testing.T) {
	assert := assert.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_, payloads, cleanup := mockTracerProvider(t, tracer.WithEnv("test_env"), tracer.WithService("test_serv"))
	tr := otel.Tracer("")
	defer cleanup()

	_, sp := tr.Start(context.Background(), "test")
	sp.End()
	tracer.Flush()
	payload, err := waitForPayload(ctx, payloads)
	if err != nil {
		t.Fatalf(err.Error())
	}
	assert.Contains(payload, "\"service\":\"test_serv\"")
	assert.Contains(payload, "\"env\":\"test_env\"")
}
