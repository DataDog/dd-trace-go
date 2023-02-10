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

// helper method to wait until timeout for test agent to receive traces
func waitForTestAgent(done chan struct{}, timeOut time.Duration, t *testing.T) {
	select {
	case <-time.After(timeOut):
		t.Log("Test agent: timed out waiting for traces")
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
			if h := r.Header.Get("X-Datadog-Trace-Count"); h == "0" {
				return
			}
			buf, err := io.ReadAll(r.Body)
			if err != nil || len(buf) == 0 {
				t.Log("Test agent: Error recieving traces")
				t.Fail()
			}
			*payload = fmt.Sprintf("%s", buf)
			done <- struct{}{}
		}
		w.WriteHeader(200)
	}))
	return NewTracerProvider(
		tracer.WithEnv(env),
		tracer.WithHTTPClient(c),
		tracer.WithService(service)), s
}

func TestSpanSetName(t *testing.T) {
	assert := assert.New(t)
	var payload string
	done := make(chan struct{})

	tp, s := getTestTracerProvider(&payload, done, "test_env", "test_srv", t)
	defer tp.Shutdown()
	defer s.Close()
	otel.SetTracerProvider(tp)
	tr := otel.Tracer("")

	_, sp := tr.Start(context.Background(), "OldName")
	sp.SetName("NewName")
	sp.End()

	tracer.Flush()
	waitForTestAgent(done, time.Second, t)
	assert.Contains(payload, "NewName")
}

func TestSpanEnd(t *testing.T) {
	assert := assert.New(t)
	testData := []struct {
		trueName        string
		falseName       string
		trueError       codes.Code
		trueErrorMsg    string
		falseError      codes.Code
		falseErrorMsg   string
		trueAttributes  map[string]string
		falseAttributes map[string]string
	}{
		{
			trueName:        "trueName",
			falseName:       "invalidName",
			trueError:       codes.Error,
			trueErrorMsg:    "error_description",
			falseError:      codes.Ok,
			falseErrorMsg:   "ok_description",
			trueAttributes:  map[string]string{"trueKey": "trueVal"},
			falseAttributes: map[string]string{"trueKey": "fakeVal", "invalidKey": "invalidVal"},
		},
	}
	var payload string
	done := make(chan struct{})

	tp, s := getTestTracerProvider(&payload, done, "test_env", "test_srv", t)
	defer tp.Shutdown()
	defer s.Close()
	otel.SetTracerProvider(tp)
	tr := otel.Tracer("")

	for _, test := range testData {
		_, sp := tr.Start(context.Background(), test.trueName)
		sp.SetStatus(codes.Error, test.trueErrorMsg)
		for k, v := range test.trueAttributes {
			sp.SetAttributes(attribute.String(k, v))
		}
		sp.End()
		// following operations should not be able to modify the Span
		sp.SetName(test.trueName)
		sp.SetStatus(test.falseError, test.falseErrorMsg)
		for k, v := range test.trueAttributes {
			sp.SetAttributes(attribute.String(k, v))
			sp.SetAttributes(attribute.String(k, v))
		}

		tracer.Flush()
		waitForTestAgent(done, time.Second, t)

		assert.Contains(payload, test.trueErrorMsg)
		assert.NotContains(payload, test.falseErrorMsg)
		assert.Contains(payload, test.trueName)
		assert.NotContains(payload, test.falseName)
		for k, v := range test.trueAttributes {
			assert.Contains(payload, k+"\xa7"+v)
		}
		for k, v := range test.falseAttributes {
			assert.NotContains(payload, k+"\xa7"+v)
		}
	}
}

func TestSpanSetStatus(t *testing.T) {
	assert := assert.New(t)
	testData := []struct {
		higherCode     codes.Code
		higherCodeDesc string
		lowerCode      codes.Code
		lowerCodeDesc  string
	}{
		{
			higherCode:     codes.Ok,
			higherCodeDesc: "ok_description",
			lowerCode:      codes.Error,
			lowerCodeDesc:  "error_description",
		},
		{
			higherCode:     codes.Error,
			higherCodeDesc: "error_description",
			lowerCode:      codes.Unset,
			lowerCodeDesc:  "unset_description",
		},
	}
	var payload string
	done := make(chan struct{})

	tp, s := getTestTracerProvider(&payload, done, "test_env", "test_srv", t)
	defer tp.Shutdown()
	defer s.Close()
	otel.SetTracerProvider(tp)
	tr := otel.Tracer("")

	for _, test := range testData {
		_, sp := tr.Start(context.Background(), "test")
		sp.SetStatus(test.higherCode, test.higherCodeDesc)
		sp.SetStatus(test.lowerCode, test.lowerCodeDesc)
		sp.End()

		tracer.Flush()
		waitForTestAgent(done, time.Second, t)

		if test.higherCode == codes.Error {
			assert.Contains(payload, test.higherCodeDesc)
		} else {
			assert.NotContains(payload, test.higherCodeDesc)
		}
		assert.NotContains(payload, test.lowerCodeDesc)

		_, sp = tr.Start(context.Background(), "test")
		sp.SetStatus(test.lowerCode, test.lowerCodeDesc)
		sp.SetStatus(test.higherCode, test.higherCodeDesc)
		sp.End()

		tracer.Flush()
		waitForTestAgent(done, time.Second, t)

		if test.higherCode == codes.Error {
			assert.Contains(payload, test.higherCodeDesc)
		} else {
			assert.NotContains(payload, test.higherCodeDesc)
		}
		assert.NotContains(payload, test.lowerCodeDesc)
	}
}

func TestSpanSetAttributes(t *testing.T) {
	assert := assert.New(t)
	var payload string
	done := make(chan struct{})

	tp, s := getTestTracerProvider(&payload, done, "test_env", "test_srv", t)
	defer tp.Shutdown()
	defer s.Close()
	otel.SetTracerProvider(tp)
	tr := otel.Tracer("")

	attributes := [][]string{{"k1", "v1_old"},
		{"k2", "v2"},
		{"k1", "v1_new"}}

	_, sp := tr.Start(context.Background(), "test")
	for _, tag := range attributes {
		sp.SetAttributes(attribute.String(tag[0], tag[1]))
	}
	sp.End()
	tracer.Flush()
	waitForTestAgent(done, time.Second, t)

	assert.Contains(payload, "k1")
	assert.Contains(payload, "k2")
	assert.Contains(payload, "v1_new")
	assert.Contains(payload, "v2")
	assert.NotContains(payload, "v1_old")
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
	assert := assert.New(t)
	var payload string
	done := make(chan struct{})

	tp, s := getTestTracerProvider(&payload, done, "test_env", "test_srv", t)
	defer tp.Shutdown()
	defer s.Close()
	otel.SetTracerProvider(tp)
	tr := otel.Tracer("")

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
