// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package extension

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/DataDog/dd-trace-go/contrib/aws/datadog-lambda-go/v2/internal/logger"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/aws/aws-lambda-go/lambdacontext"
	"github.com/stretchr/testify/assert"
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

func TestExtensionStartInvokeLambdaRequestId(t *testing.T) {
	headers := http.Header{}
	capturingClient := capturingClient{hdr: headers}

	em := &ExtensionManager{
		startInvocationUrl: startInvocationUrl,
		httpClient:         capturingClient,
	}

	lc := &lambdacontext.LambdaContext{
		AwsRequestID: "test-request-id-12345",
	}
	ctx := lambdacontext.NewContext(context.TODO(), lc)
	em.SendStartInvocationRequest(ctx, []byte{})

	err := em.Flush()

	assert.Nil(t, err)
	assert.Equal(t, "test-request-id-12345", headers.Get("lambda-runtime-aws-request-id"))
}

func TestExtensionStartInvokeLambdaRequestIdError(t *testing.T) {
	em := &ExtensionManager{
		startInvocationUrl: startInvocationUrl,
		httpClient:         &ClientSuccessStartInvoke{},
	}

	logOutput := captureLog(func() { em.SendStartInvocationRequest(context.TODO(), []byte{}) })
	err := em.Flush()
	assert.Nil(t, err)
	assert.Contains(t, logOutput, "missing AWS Lambda context. Unable to set lambda-runtime-aws-request-id header")
	lines := strings.Split(strings.TrimSpace(logOutput), "\n")
	assert.Equal(t, 1, len(lines))
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
	ctx := lambdacontext.NewContext(context.TODO(), &lambdacontext.LambdaContext{})
	span := tracer.StartSpan("aws.lambda")
	logOutput := captureLog(func() { em.SendEndInvocationRequest(ctx, span, tracer.FinishConfig{}) })
	span.Finish()

	assert.Equal(t, "", logOutput)
}

func TestExtensionEndInvokeLambdaRequestId(t *testing.T) {
	headers := http.Header{}
	capturingClient := capturingClient{hdr: headers}

	em := &ExtensionManager{
		endInvocationUrl: endInvocationUrl,
		httpClient:       capturingClient,
	}

	lc := &lambdacontext.LambdaContext{
		AwsRequestID: "test-request-id-12345",
	}

	ctx := lambdacontext.NewContext(context.TODO(), lc)
	span := tracer.StartSpan("aws.lambda")
	span.Finish()
	cfg := tracer.FinishConfig{}
	em.SendEndInvocationRequest(ctx, span, cfg)
	err := em.Flush()
	assert.Nil(t, err)
	assert.Equal(t, "test-request-id-12345", headers.Get("lambda-runtime-aws-request-id"))
}

func TestExtensionEndInvokeLambdaRequestIdError(t *testing.T) {
	headers := http.Header{}
	capturingClient := capturingClient{hdr: headers}
	ctx := context.WithValue(context.TODO(), DdSamplingPriority, mockSamplingPriority)
	ctx = context.WithValue(ctx, DdTraceId, mockTraceId)
	em := &ExtensionManager{
		endInvocationUrl: endInvocationUrl,
		httpClient:       capturingClient,
	}

	span := tracer.StartSpan("aws.lambda")
	logOutput := captureLog(func() { em.SendEndInvocationRequest(ctx, span, tracer.FinishConfig{}) })
	span.Finish()

	err := em.Flush()
	assert.Nil(t, err)
	assert.Contains(t, logOutput, "missing AWS Lambda context. Unable to set lambda-runtime-aws-request-id header")
	lines := strings.Split(strings.TrimSpace(logOutput), "\n")
	assert.Equal(t, 1, len(lines))
}

func TestExtensionEndInvocationError(t *testing.T) {
	em := &ExtensionManager{
		endInvocationUrl: endInvocationUrl,
		httpClient:       &ClientErrorMock{},
	}
	span := tracer.StartSpan("aws.lambda")
	logOutput := captureLog(func() { em.SendEndInvocationRequest(context.TODO(), span, tracer.FinishConfig{}) })
	span.Finish()

	assert.Contains(t, logOutput, "could not send end invocation payload to the extension")
}

func TestExtensionEndInvocationSamplingPriority(t *testing.T) {
	// Start the tracer to ensure proper sampling priority handling
	err := tracer.Start()
	if err != nil {
		t.Fatalf("Failed to start tracer: %v", err)
	}
	defer tracer.Stop()

	// Create a real span and set its sampling priority to -1 using ManualDrop
	span := tracer.StartSpan("test")
	defer span.Finish()

	// When priority in context, use that value
	headers1 := http.Header{}
	em1 := &ExtensionManager{httpClient: capturingClient{hdr: headers1}}
	ctx := context.WithValue(context.Background(), DdTraceId, "123")
	ctx = context.WithValue(ctx, DdSamplingPriority, "2")
	em1.SendEndInvocationRequest(ctx, span, tracer.FinishConfig{})
	assert.Equal(t, "2", headers1.Get("X-Datadog-Sampling-Priority"))

	// When no context, get priority from span
	// Set sampling priority to -1 using ManualDrop tag
	span.SetTag(ext.ManualDrop, true)
	headers2 := http.Header{}
	em2 := &ExtensionManager{httpClient: capturingClient{hdr: headers2}}
	em2.SendEndInvocationRequest(context.Background(), span, tracer.FinishConfig{})
	assert.Equal(t, "-1", headers2.Get("X-Datadog-Sampling-Priority"))
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
	span := tracer.StartSpan("aws.lambda")
	cfg := tracer.FinishConfig{Error: fmt.Errorf("ooooops")}

	em.SendEndInvocationRequest(context.TODO(), span, cfg)

	assert.Equal(t, hdr.Get("X-Datadog-Invocation-Error"), "true")
	assert.Equal(t, hdr.Get("X-Datadog-Invocation-Error-Msg"), "ooooops")
	assert.Equal(t, hdr.Get("X-Datadog-Invocation-Error-Type"), "*errors.errorString")

	data, err := base64.StdEncoding.DecodeString(hdr.Get("X-Datadog-Invocation-Error-Stack"))
	assert.Nil(t, err)
	assert.Contains(t, string(data), "github.com/DataDog/dd-trace-go/contrib/aws/datadog-lambda-go/v2")
	assert.Contains(t, string(data), "TestExtensionEndInvocationErrorHeaders")
}

func TestExtensionEndInvocationErrorHeadersNilError(t *testing.T) {
	hdr := http.Header{}
	em := &ExtensionManager{httpClient: capturingClient{hdr: hdr}}
	span := tracer.StartSpan("aws.lambda")
	cfg := tracer.FinishConfig{Error: nil}

	em.SendEndInvocationRequest(context.TODO(), span, cfg)

	assert.Equal(t, hdr.Get("X-Datadog-Invocation-Error"), "")
	assert.Equal(t, hdr.Get("X-Datadog-Invocation-Error-Msg"), "")
	assert.Equal(t, hdr.Get("X-Datadog-Invocation-Error-Type"), "")
	assert.Equal(t, hdr.Get("X-Datadog-Invocation-Error-Stack"), "")
}
