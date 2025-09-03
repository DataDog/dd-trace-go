/*
 * Unless explicitly stated otherwise all files in this repository are licensed
 * under the Apache License Version 2.0.
 *
 * This product includes software developed at Datadog (https://www.datadoghq.com/).
 * Copyright 2021 Datadog, Inc.
 */

package extension

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/contrib/aws/datadog-lambda-go/internal/logger"
	"github.com/stretchr/testify/assert"
	"github.com/DataDog/dd-trace-go/v2/ddtrace"
	ddtracer "github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

type ClientErrorMock struct {
}

type ClientSuccessMock struct {
}

type ClientSuccess202Mock struct {
}

type ClientSuccessStartInvoke struct {
	headers http.Header
}

type ClientSuccessEndInvoke struct {
}

const (
	mockTraceId          = "1"
	mockParentId         = "2"
	mockSamplingPriority = "3"
)

func (c *ClientErrorMock) Do(req *http.Request) (*http.Response, error) {
	return nil, fmt.Errorf("KO")
}

func (c *ClientSuccessMock) Do(req *http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: 200}, nil
}

func (c *ClientSuccess202Mock) Do(req *http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: 202, Status: "KO"}, nil
}

func (c *ClientSuccessStartInvoke) Do(req *http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: 200, Status: "KO", Header: c.headers}, nil
}

func (c *ClientSuccessEndInvoke) Do(req *http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: 200, Status: "KO"}, nil
}

func captureLog(f func()) string {
	var buf bytes.Buffer
	logger.SetOutput(&buf)
	f()
	logger.SetOutput(os.Stdout)
	return buf.String()
}

func TestBuildExtensionManager(t *testing.T) {
	em := BuildExtensionManager(false)
	assert.Equal(t, "http://localhost:8124/lambda/hello", em.helloRoute)
	assert.Equal(t, "http://localhost:8124/lambda/flush", em.flushRoute)
	assert.Equal(t, "http://localhost:8124/lambda/start-invocation", em.startInvocationUrl)
	assert.Equal(t, "http://localhost:8124/lambda/end-invocation", em.endInvocationUrl)
	assert.Equal(t, "/opt/extensions/datadog-agent", em.extensionPath)
	assert.Equal(t, false, em.isUniversalInstrumentation)
	assert.NotNil(t, em.httpClient)
}

func TestIsAgentRunningFalse(t *testing.T) {
	em := &ExtensionManager{
		httpClient: &ClientErrorMock{},
	}
	assert.False(t, em.IsExtensionRunning())
}

func TestIsAgentRunningFalseSinceTheAgentIsNotHere(t *testing.T) {
	em := &ExtensionManager{
		extensionPath: "/impossible/path/test",
	}
	em.checkAgentRunning()
	assert.False(t, em.IsExtensionRunning())
}

func TestIsAgentRunningTrue(t *testing.T) {
	existingPath, err := os.Getwd()
	assert.Nil(t, err)

	em := &ExtensionManager{
		httpClient:    &ClientSuccessMock{},
		extensionPath: existingPath,
	}
	em.checkAgentRunning()
	assert.True(t, em.IsExtensionRunning())
}

func TestFlushErrorNot200(t *testing.T) {
	em := &ExtensionManager{
		httpClient: &ClientSuccess202Mock{},
	}
	err := em.Flush()
	assert.Equal(t, "the Agent didn't returned HTTP 200: KO", err.Error())
}

func TestFlushError(t *testing.T) {
	em := &ExtensionManager{
		httpClient: &ClientErrorMock{},
	}
	err := em.Flush()
	assert.Equal(t, "was not able to reach the Agent to flush: KO", err.Error())
}

func TestFlushSuccess(t *testing.T) {
	em := &ExtensionManager{
		httpClient: &ClientSuccessMock{},
	}
	err := em.Flush()
	assert.Nil(t, err)
}

func TestExtensionStartInvoke(t *testing.T) {
	em := &ExtensionManager{
		startInvocationUrl: startInvocationUrl,
		httpClient:         &ClientSuccessStartInvoke{},
	}
	ctx := em.SendStartInvocationRequest(context.TODO(), []byte{})
	traceId := ctx.Value(DdTraceId)
	parentId := ctx.Value(DdParentId)
	samplingPriority := ctx.Value(DdSamplingPriority)
	err := em.Flush()

	assert.Nil(t, err)
	assert.Nil(t, traceId)
	assert.Nil(t, parentId)
	assert.Nil(t, samplingPriority)
}

func TestExtensionStartInvokeWithTraceContext(t *testing.T) {
	headers := http.Header{}
	headers.Set(string(DdTraceId), mockTraceId)
	headers.Set(string(DdParentId), mockParentId)
	headers.Set(string(DdSamplingPriority), mockSamplingPriority)

	em := &ExtensionManager{
		startInvocationUrl: startInvocationUrl,
		httpClient: &ClientSuccessStartInvoke{
			headers: headers,
		},
	}
	ctx := em.SendStartInvocationRequest(context.TODO(), []byte{})
	traceId := ctx.Value(DdTraceId)
	parentId := ctx.Value(DdParentId)
	samplingPriority := ctx.Value(DdSamplingPriority)
	err := em.Flush()

	assert.Nil(t, err)
	assert.Equal(t, mockTraceId, traceId)
	assert.Equal(t, mockParentId, parentId)
	assert.Equal(t, mockSamplingPriority, samplingPriority)
}

func TestExtensionStartInvokeWithTraceContextNoParentID(t *testing.T) {
	headers := http.Header{}
	headers.Set(string(DdTraceId), mockTraceId)
	headers.Set(string(DdSamplingPriority), mockSamplingPriority)

	em := &ExtensionManager{
		startInvocationUrl: startInvocationUrl,
		httpClient: &ClientSuccessStartInvoke{
			headers: headers,
		},
	}
	ctx := em.SendStartInvocationRequest(context.TODO(), []byte{})
	traceId := ctx.Value(DdTraceId)
	parentId := ctx.Value(DdParentId)
	samplingPriority := ctx.Value(DdSamplingPriority)
	err := em.Flush()

	assert.Nil(t, err)
	assert.Equal(t, mockTraceId, traceId)
	assert.Equal(t, mockTraceId, parentId)
	assert.Equal(t, mockSamplingPriority, samplingPriority)
}

func TestExtensionEndInvocation(t *testing.T) {
	em := &ExtensionManager{
		endInvocationUrl: endInvocationUrl,
		httpClient:       &ClientSuccessEndInvoke{},
	}
	span := ddtracer.StartSpan("aws.lambda")
	logOutput := captureLog(func() { em.SendEndInvocationRequest(context.TODO(), span, ddtracer.FinishConfig{}) })
	span.Finish()

	assert.Equal(t, "", logOutput)
}

func TestExtensionEndInvocationError(t *testing.T) {
	em := &ExtensionManager{
		endInvocationUrl: endInvocationUrl,
		httpClient:       &ClientErrorMock{},
	}
	span := ddtracer.StartSpan("aws.lambda")
	logOutput := captureLog(func() { em.SendEndInvocationRequest(context.TODO(), span, ddtracer.FinishConfig{}) })
	span.Finish()

	assert.Contains(t, logOutput, "could not send end invocation payload to the extension")
}

type mockSpanContext struct {
	ddtrace.SpanContext
}

func (m mockSpanContext) TraceID() string               { return "123" }
func (m mockSpanContext) TraceIDBytes() [16]byte        { return [16]byte{} }
func (m mockSpanContext) TraceIDLower() uint64          { return 123 }
func (m mockSpanContext) SpanID() uint64                { return 456 }
func (m mockSpanContext) SamplingPriority() (int, bool) { return -1, true }
func (m mockSpanContext) ForeachBaggageItem(handler func(k, v string) bool) {}

type mockSpan struct{ ddtrace.Span }

func (m mockSpan) Context() ddtrace.SpanContext { return mockSpanContext{} }

func TestExtensionEndInvocationSamplingPriority(t *testing.T) {
	headers := http.Header{}
	em := &ExtensionManager{httpClient: capturingClient{hdr: headers}}
	span := &mockSpan{}

	// When priority in context, use that value
	ctx := context.WithValue(context.Background(), DdTraceId, "123")
	ctx = context.WithValue(ctx, DdSamplingPriority, "2")
	em.SendEndInvocationRequest(ctx, span, ddtracer.FinishConfig{})
	assert.Equal(t, "2", headers.Get("X-Datadog-Sampling-Priority"))

	// When no context, get priority from span
	em.SendEndInvocationRequest(context.Background(), span, ddtracer.FinishConfig{})
	assert.Equal(t, "-1", headers.Get("X-Datadog-Sampling-Priority"))
}

type capturingClient struct {
	hdr http.Header
}

func (c capturingClient) Do(req *http.Request) (*http.Response, error) {
	for k, v := range req.Header {
		c.hdr[k] = v
	}
	return &http.Response{StatusCode: 200}, nil
}

func TestExtensionEndInvocationErrorHeaders(t *testing.T) {
	hdr := http.Header{}
	em := &ExtensionManager{httpClient: capturingClient{hdr: hdr}}
	span := ddtracer.StartSpan("aws.lambda")
	cfg := ddtracer.FinishConfig{Error: fmt.Errorf("ooooops")}

	em.SendEndInvocationRequest(context.TODO(), span, cfg)

	assert.Equal(t, hdr.Get("X-Datadog-Invocation-Error"), "true")
	assert.Equal(t, hdr.Get("X-Datadog-Invocation-Error-Msg"), "ooooops")
	assert.Equal(t, hdr.Get("X-Datadog-Invocation-Error-Type"), "*errors.errorString")

	data, err := base64.StdEncoding.DecodeString(hdr.Get("X-Datadog-Invocation-Error-Stack"))
	assert.Nil(t, err)
	assert.Contains(t, string(data), "github.com/DataDog/dd-trace-go/v2/contrib/aws/datadog-lambda-go")
	assert.Contains(t, string(data), "TestExtensionEndInvocationErrorHeaders")
}

func TestExtensionEndInvocationErrorHeadersNilError(t *testing.T) {
	hdr := http.Header{}
	em := &ExtensionManager{httpClient: capturingClient{hdr: hdr}}
	span := ddtracer.StartSpan("aws.lambda")
	cfg := ddtracer.FinishConfig{Error: nil}

	em.SendEndInvocationRequest(context.TODO(), span, cfg)

	assert.Equal(t, hdr.Get("X-Datadog-Invocation-Error"), "")
	assert.Equal(t, hdr.Get("X-Datadog-Invocation-Error-Msg"), "")
	assert.Equal(t, hdr.Get("X-Datadog-Invocation-Error-Type"), "")
	assert.Equal(t, hdr.Get("X-Datadog-Invocation-Error-Stack"), "")
}
