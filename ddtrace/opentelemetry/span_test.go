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

	"go.opentelemetry.io/otel/attribute"

	"github.com/stretchr/testify/assert"
	"github.com/tinylib/msgp/msgp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"

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
				t.Log("Test agent: Error receiving traces")
				t.Fail()
			}
			var js bytes.Buffer
			msgp.UnmarshalAsJSON(&js, buf)
			select {
			case payloads <- js.String():
			default:
				t.Log("Test agent: no one to receive payloads")
			}
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

func waitForPayload(ctx context.Context, t *testing.T, payloads chan string) string {
	var p string
	select {
	case <-ctx.Done():
		t.Fatal("timed out waiting for traces")
	case p = <-payloads:
		break
	}
	return p
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
	p := waitForPayload(ctx, t, payloads)
	assert.Contains(p, "NewName")
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
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_, payloads, cleanup := mockTracerProvider(t)
	tr := otel.Tracer("")
	defer cleanup()

	for _, test := range testData {
		_, sp := tr.Start(context.Background(), test.trueName)
		sp.SetStatus(codes.Error, test.trueErrorMsg)
		for k, v := range test.trueAttributes {
			sp.SetAttributes(attribute.String(k, v))
		}
		assert.True(sp.IsRecording())
		sp.End()
		assert.False(sp.IsRecording())
		// following operations should not be able to modify the Span
		sp.SetName(test.trueName)
		sp.SetStatus(test.falseError, test.falseErrorMsg)
		for k, v := range test.trueAttributes {
			sp.SetAttributes(attribute.String(k, v))
			sp.SetAttributes(attribute.String(k, v))
		}

		tracer.Flush()
		payload := waitForPayload(ctx, t, payloads)

		assert.Contains(payload, test.trueErrorMsg)
		assert.NotContains(payload, test.falseErrorMsg)
		assert.Contains(payload, test.trueName)
		assert.NotContains(payload, test.falseName)
		for k, v := range test.trueAttributes {
			assert.Contains(payload, fmt.Sprintf("\"%s\":\"%s\"", k, v))
		}
		for k, v := range test.falseAttributes {
			assert.NotContains(payload, fmt.Sprintf("\"%s\":\"%s\"", k, v))
		}
	}
}

func TestSpanEndOptions(t *testing.T) {
	assert := assert.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, payloads, cleanup := mockTracerProvider(t)
	tr := otel.Tracer("")
	defer cleanup()

	startTime := time.Now()
	duration := time.Second * 5
	_, sp := tr.Start(
		ContextWithStartOptions(context.Background(),
			tracer.ResourceName("ctx_rsc"),
			tracer.ServiceName("ctx_srv"),
			tracer.StartTime(startTime),
			tracer.WithSpanID(1234567890),
		), "op_name")
	EndOptions(sp, tracer.FinishTime(startTime.Add(duration)))
	sp.End()

	tracer.Flush()
	p := waitForPayload(ctx, t, payloads)
	assert.Contains(p, "ctx_srv")
	assert.Contains(p, "ctx_rsc")
	assert.Contains(p, "1234567890")
	assert.Contains(p, fmt.Sprint(startTime.UnixNano()))
	assert.Contains(p, fmt.Sprint(duration.Nanoseconds()))
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
	_, payloads, cleanup := mockTracerProvider(t)
	tr := otel.Tracer("")
	defer cleanup()

	for _, test := range testData {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_, sp := tr.Start(context.Background(), "test")
		sp.SetStatus(test.higherCode, test.higherCodeDesc)
		sp.SetStatus(test.lowerCode, test.lowerCodeDesc)
		sp.End()

		tracer.Flush()
		payload := waitForPayload(ctx, t, payloads)

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
		payload = waitForPayload(ctx, t, payloads)

		if test.higherCode == codes.Error {
			assert.Contains(payload, test.higherCodeDesc)
		} else {
			assert.NotContains(payload, test.higherCodeDesc)
		}
		assert.NotContains(payload, test.lowerCodeDesc)
		cancel()
	}
}

func TestSpanContextWithStartOptions(t *testing.T) {
	assert := assert.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
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
	sp.End()

	tracer.Flush()
	p := waitForPayload(ctx, t, payloads)
	assert.Contains(p, "ctx_srv")
	assert.Contains(p, "ctx_rsc")
	assert.Contains(p, "1234567890")
	assert.Contains(p, fmt.Sprint(startTime.UnixNano()))
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
	payload := waitForPayload(ctx, t, payloads)

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
	payload := waitForPayload(ctx, t, payloads)
	assert.Contains(payload, "\"service\":\"test_serv\"")
	assert.Contains(payload, "\"env\":\"test_env\"")
}
