// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package trace

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/DataDog/dd-trace-go/v2/contrib/aws/datadog-lambda-go/internal/extension"
	"github.com/DataDog/dd-trace-go/v2/contrib/aws/datadog-lambda-go/internal/logger"
	"github.com/DataDog/dd-trace-go/v2/contrib/aws/datadog-lambda-go/internal/version"
	"github.com/DataDog/dd-trace-go/v2/ddtrace"
	ddotel "github.com/DataDog/dd-trace-go/v2/ddtrace/opentelemetry"
	ddtracer "github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/aws/aws-lambda-go/lambdacontext"
	"go.opentelemetry.io/otel"
)

type (
	// Listener creates a function execution span and injects it into the context
	Listener struct {
		ddTraceEnabled           bool
		mergeXrayTraces          bool
		universalInstrumentation bool
		otelTracerEnabled        bool
		extensionManager         *extension.ExtensionManager
		traceContextExtractor    ContextExtractor
		tracerOptions            []ddtracer.StartOption
	}

	// Config gives options for how the Listener should work
	Config struct {
		DDTraceEnabled           bool
		MergeXrayTraces          bool
		UniversalInstrumentation bool
		OtelTracerEnabled        bool
		TraceContextExtractor    ContextExtractor
		TracerOptions            []ddtracer.StartOption
	}
)

// The function execution span is the top-level span representing the current Lambda function execution
var functionExecutionSpan *ddtracer.Span

var tracerInitialized = false

// MakeListener initializes a new trace lambda Listener
func MakeListener(config Config, extensionManager *extension.ExtensionManager) Listener {

	return Listener{
		ddTraceEnabled:           config.DDTraceEnabled,
		mergeXrayTraces:          config.MergeXrayTraces,
		universalInstrumentation: config.UniversalInstrumentation,
		otelTracerEnabled:        config.OtelTracerEnabled,
		extensionManager:         extensionManager,
		traceContextExtractor:    config.TraceContextExtractor,
		tracerOptions:            config.TracerOptions,
	}
}

// HandlerStarted sets up tracing and starts the function execution span if Datadog tracing is enabled
func (l *Listener) HandlerStarted(ctx context.Context, msg json.RawMessage) context.Context {
	if !l.ddTraceEnabled {
		return ctx
	}

	if l.universalInstrumentation && l.extensionManager.IsExtensionRunning() {
		ctx = l.extensionManager.SendStartInvocationRequest(ctx, msg)
	}

	ctx, _ = contextWithRootTraceContext(ctx, msg, l.mergeXrayTraces, l.traceContextExtractor)

	if !tracerInitialized {
		serviceName := os.Getenv("DD_SERVICE")
		if serviceName == "" {
			serviceName = "aws.lambda"
		}
		extensionNotRunning := !l.extensionManager.IsExtensionRunning()
		opts := append([]ddtracer.StartOption{
			ddtracer.WithService(serviceName),
			ddtracer.WithLambdaMode(extensionNotRunning),
			ddtracer.WithGlobalTag("_dd.origin", "lambda"),
			ddtracer.WithSendRetries(2),
		}, l.tracerOptions...)
		if l.otelTracerEnabled {
			provider := ddotel.NewTracerProvider(
				opts...,
			)
			otel.SetTracerProvider(provider)
		} else {
			ddtracer.Start(
				opts...,
			)
		}
		tracerInitialized = true
	}

	isDdServerlessSpan := l.universalInstrumentation && l.extensionManager.IsExtensionRunning()
	functionExecutionSpan, ctx = startFunctionExecutionSpan(ctx, l.mergeXrayTraces, isDdServerlessSpan)

	// Add the span to the context so the user can create child spans
	ctx = ddtracer.ContextWithSpan(ctx, functionExecutionSpan)

	return ctx
}

// HandlerFinished ends the function execution span and stops the tracer
func (l *Listener) HandlerFinished(ctx context.Context, err error) {
	if functionExecutionSpan != nil {
		functionExecutionSpan.Finish(ddtracer.WithError(err))

		finishConfig := ddtracer.FinishConfig{Error: err}

		if l.universalInstrumentation && l.extensionManager.IsExtensionRunning() {
			l.extensionManager.SendEndInvocationRequest(ctx, functionExecutionSpan, finishConfig)
		}
	}

	ddtracer.Flush()
}

// startFunctionExecutionSpan starts a span that represents the current Lambda function execution
// and returns the span so that it can be finished when the function execution is complete
func startFunctionExecutionSpan(ctx context.Context, mergeXrayTraces bool, isDdServerlessSpan bool) (*ddtracer.Span, context.Context) {
	// Extract information from context
	lambdaCtx, _ := lambdacontext.FromContext(ctx)
	rootTraceContext, ok := ctx.Value(traceContextKey).(TraceContext)
	if !ok {
		logger.Error(fmt.Errorf("Error extracting trace context from context object"))
	}

	functionArn := lambdaCtx.InvokedFunctionArn
	functionArn = strings.ToLower(functionArn)
	functionArn, functionVersion := separateVersionFromFunctionArn(functionArn)

	// Set the root trace context as the parent of the function execution span
	var parentSpanContext ddtrace.SpanContext
	convertedSpanContext, err := ConvertTraceContextToSpanContext(rootTraceContext)
	if err == nil {
		parentSpanContext = convertedSpanContext
	}

	resourceName := lambdacontext.FunctionName
	if isDdServerlessSpan {
		// The extension will drop this span, prioritizing the execution span the extension creates
		resourceName = string(extension.DdSeverlessSpan)
	}

	spanOptions := []ddtracer.StartSpanOption{
		ddtracer.SpanType("serverless"),
		ddtracer.ResourceName(resourceName),
		ddtracer.Tag("cold_start", ctx.Value("cold_start")),
		ddtracer.Tag("function_arn", functionArn),
		ddtracer.Tag("function_version", functionVersion),
		ddtracer.Tag("request_id", lambdaCtx.AwsRequestID),
		ddtracer.Tag("resource_names", lambdacontext.FunctionName),
		ddtracer.Tag("functionname", strings.ToLower(lambdacontext.FunctionName)),
		ddtracer.Tag("datadog_lambda", version.DDLambdaVersion),
		ddtracer.Tag("dd_trace", version.DDTraceVersion),
	}

	if parentSpanContext != nil {
		if sc, ok := parentSpanContext.(*ddtracer.SpanContext); ok {
			spanOptions = append(spanOptions, ddtracer.ChildOf(sc))
		}
	}

	span := ddtracer.StartSpan(
		"aws.lambda", // This operation name will be replaced with the value of the service tag by the Forwarder
		spanOptions...,
	)

	if parentSpanContext != nil && mergeXrayTraces {
		// This tag will cause the Forwarder to drop the span (to avoid redundancy with X-Ray)
		span.SetTag("_dd.parent_source", "xray")
	}

	ctx = context.WithValue(ctx, extension.DdSpanId, fmt.Sprint(span.Context().SpanID()))

	return span, ctx
}

func separateVersionFromFunctionArn(functionArn string) (arnWithoutVersion string, functionVersion string) {
	arnSegments := strings.Split(functionArn, ":")
	if cap(arnSegments) < 7 {
		return "", ""
	}
	functionVersion = "$LATEST"
	arnWithoutVersion = strings.Join(arnSegments[0:7], ":")
	if len(arnSegments) > 7 {
		functionVersion = arnSegments[7]
	}
	return arnWithoutVersion, functionVersion
}
