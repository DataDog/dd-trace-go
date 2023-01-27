// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package opentelemetry

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/httpmem"
)

func waitForTestAgent(done chan struct{}, timeOut time.Duration, t *testing.T) {
	select {
	case <-time.After(timeOut):
		t.FailNow()
	case <-done:
		break
	}
}

// initializes a test trace provider and agent server
func getTestTracerProvider(payload *string, done chan struct{},
	env string, service string, t *testing.T) (*TracerProvider, *http.Server) {
	s, c := httpmem.ServerAndClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v0.4/traces":
			buf, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fail()
			}
			*payload = fmt.Sprintf("%s", buf)
			done <- struct{}{}
		}
		w.WriteHeader(200)
	}))
	tp := NewTracerProvider(
		tracer.WithEnv(env),
		tracer.WithHTTPClient(c),
		tracer.WithService(service))
	return tp, s
}

func TestSpanSetName(t *testing.T) {
	var payload string
	done := make(chan struct{})

	tp, s := getTestTracerProvider(&payload, done, "test_env", "test_srv", t)
	defer s.Close()
	otel.SetTracerProvider(tp)
	tr := otel.Tracer("")
	defer tp.Shutdown()
	assert := assert.New(t)

	_, sp := tr.Start(context.Background(), "OldName")
	// SetName sets the name but not the resource in span - bug?
	sp.SetName("NewName")
	sp.End()

	tracer.Flush()
	waitForTestAgent(done, time.Second, t)

	assert.Contains(payload, "NewName")
	// following assert fails because "resource" in span is not set by SetName
	// assert.NotContains(payload, "OldName")
}

func TestSpanEnd(t *testing.T) {

	var payload string
	done := make(chan struct{})

	tp, s := getTestTracerProvider(&payload, done, "test_env", "test_srv", t)
	defer s.Close()
	otel.SetTracerProvider(tp)
	tr := otel.Tracer("")
	defer tp.Shutdown()
	assert := assert.New(t)

	_, sp := tr.Start(context.Background(), "OldName")
	sp.SetStatus(codes.Error, "error_description")
	sp.SetAttributes(attribute.String("span_end_key", "span_end_val"))
	sp.End()
	// following operations should not be able to modify the Span
	sp.SetStatus(codes.Ok, "ok_description")
	sp.SetAttributes(attribute.String("span_end", "after_end"))
	sp.SetAttributes(attribute.String("key1", "val1"))
	sp.SetName("NewName")

	tracer.Flush()
	waitForTestAgent(done, time.Second, t)

	assert.Contains(payload, "error_description")
	assert.Contains(payload, "span_end_val")
	assert.Contains(payload, "OldName")

	assert.NotContains(payload, "after_end")
	assert.NotContains(payload, "key1")
	assert.NotContains(payload, "val1")
	assert.NotContains(payload, "NewName")
}

func TestSpanSetStatus(t *testing.T) {
	var payload string
	done := make(chan struct{})

	tp, s := getTestTracerProvider(&payload, done, "test_env", "test_srv", t)
	defer s.Close()
	otel.SetTracerProvider(tp)
	tr := otel.Tracer("")
	defer tp.Shutdown()
	assert := assert.New(t)

	_, sp := tr.Start(context.Background(), "SpanEndTest")
	sp.SetStatus(codes.Error, "error_description")
	// following operation should not do anything
	sp.SetStatus(codes.Unset, "unset_description")
	sp.End()

	tracer.Flush()
	waitForTestAgent(done, time.Second, t)

	assert.Contains(payload, "error_description")
	assert.NotContains(payload, "unset_description")
}

func TestSpanSetAttributes(t *testing.T) {
	var payload string
	done := make(chan struct{})

	tp, s := getTestTracerProvider(&payload, done, "test_env", "test_srv", t)
	defer s.Close()
	otel.SetTracerProvider(tp)
	tr := otel.Tracer("")
	defer tp.Shutdown()

	assert := assert.New(t)

	attributes := [][]string{{"SpanAttrK1", "SpanAttrV1Old"},
		{"SpanAttrK2", "SpanAttrV2"},
		{"SpanAttrK1", "SpanAttrV1New"}}

	_, sp := tr.Start(context.Background(), "testSpan")
	for _, tag := range attributes {
		sp.SetAttributes(attribute.String(tag[0], tag[1]))
	}
	sp.End()

	tracer.Flush()
	waitForTestAgent(done, time.Second, t)

	// check for keys
	assert.Contains(payload, "SpanAttrK1")
	assert.Contains(payload, "SpanAttrK2")
	// check for valid values
	assert.Contains(payload, "SpanAttrV1New")
	assert.Contains(payload, "SpanAttrV2")
	assert.NotContains(payload, "SpanAttrV1Old")
}

func TestSpanMethods(t *testing.T) {
	testData := struct {
		env, srv, oldOp, newOp string
		tags                   [][]string // key - tags[0][0], value - tags[0][1]
	}{
		env:   "test_env",
		srv:   "test_srv",
		oldOp: "old_op",
		newOp: "new_op",
		tags:  [][]string{{"tag_1", "tag_1_val"}, {"opt_tag_1", "opt_tag_1_val"}}}
	done := make(chan struct{})
	var payload string
	s, c := httpmem.ServerAndClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v0.4/traces":
			buf, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fail()
			}
			payload = fmt.Sprintf("%s", buf)
			done <- struct{}{}
		}
		w.WriteHeader(200)
	}))
	defer s.Close()

	otel.SetTracerProvider(NewTracerProvider(
		tracer.WithEnv(testData.env),
		tracer.WithHTTPClient(c),
		tracer.WithService(testData.srv)))
	tr := otel.Tracer("")
	assert := assert.New(t)

	_, sp := tr.Start(context.Background(), testData.oldOp)
	for _, tag := range testData.tags {
		sp.SetAttributes(attribute.String(tag[0], tag[1]))
	}
	sp.SetName(testData.newOp)
	// Should return true as long as span is not finished
	assert.True(sp.IsRecording())
	sp.SetStatus(codes.Error, "error_description")
	sp.End()
	// Should return false once the span is finished / ended
	assert.False(sp.IsRecording())
	tracer.Flush()

	select {
	case <-time.After(time.Second):
		t.FailNow()
	case <-done:
		break
	}
	assert.Contains(payload, testData.env)
	assert.Contains(payload, testData.newOp)
	assert.Contains(payload, testData.srv)
	for _, tag := range testData.tags {
		assert.Contains(payload, tag[0])
		assert.Contains(payload, tag[1])
	}
}
