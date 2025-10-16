// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package trace

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/DataDog/dd-trace-go/contrib/aws/datadog-lambda-go/v2/internal/extension"
	"github.com/DataDog/dd-trace-go/contrib/aws/datadog-lambda-go/v2/internal/logger"
	"github.com/DataDog/dd-trace-go/v2/ddtrace"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/aws/aws-xray-sdk-go/v2/header"
	"github.com/aws/aws-xray-sdk-go/v2/xray"
)

type (
	eventWithHeaders struct {
		Headers           map[string]string   `json:"headers"`
		MultiValueHeaders map[string][]string `json:"multiValueHeaders"`
	}

	// TraceContext is map of headers containing a Datadog trace context.
	TraceContext map[string]string

	// ContextExtractor is a func type for extracting a root TraceContext.
	ContextExtractor func(ctx context.Context, ev json.RawMessage) map[string]string
)

type contextKeytype int

// traceContextKey is the key used to store a TraceContext in a TraceContext object
var traceContextKey = new(contextKeytype)

// DefaultTraceExtractor is the default trace extractor. Extracts root trace from API Gateway headers.
var DefaultTraceExtractor = getHeadersFromEventHeaders

// contextWithRootTraceContext uses the incoming event and context object payloads to determine
// the root TraceContext and then adds that TraceContext to the context object.
func contextWithRootTraceContext(ctx context.Context, ev json.RawMessage, mergeXrayTraces bool, extractor ContextExtractor) (context.Context, error) {
	datadogTraceContext, gotDatadogTraceContext := getTraceContext(ctx, extractor(ctx, ev))

	xrayTraceContext, errGettingXrayContext := convertXrayTraceContextFromLambdaContext(ctx)
	if errGettingXrayContext != nil {
		logger.Error(fmt.Errorf("Couldn't convert X-Ray trace context: %v", errGettingXrayContext))
	}

	if gotDatadogTraceContext && errGettingXrayContext == nil {
		err := createDummySubsegmentForXrayConverter(ctx, datadogTraceContext)
		if err != nil {
			logger.Error(fmt.Errorf("Couldn't create segment: %v", err))
		}
	}

	if !mergeXrayTraces {
		logger.Debug("Merge X-Ray Traces is off, using trace context from Datadog only")
		return context.WithValue(ctx, traceContextKey, datadogTraceContext), nil
	}

	if !gotDatadogTraceContext {
		logger.Debug("Merge X-Ray Traces is on, but did not get incoming Datadog trace context; using X-Ray trace context instead")
		return context.WithValue(ctx, traceContextKey, xrayTraceContext), nil
	}

	logger.Debug("Using merged Datadog/X-Ray trace context")
	mergedTraceContext := TraceContext{}
	mergedTraceContext[traceIDHeader] = datadogTraceContext[traceIDHeader]
	mergedTraceContext[samplingPriorityHeader] = datadogTraceContext[samplingPriorityHeader]
	mergedTraceContext[parentIDHeader] = xrayTraceContext[parentIDHeader]
	return context.WithValue(ctx, traceContextKey, mergedTraceContext), nil
}

// ConvertCurrentXrayTraceContext returns the current X-Ray trace context converted to Datadog headers, taking into account
// the current subsegment. It is designed for sending Datadog trace headers from functions instrumented with the X-Ray SDK.
func ConvertCurrentXrayTraceContext(ctx context.Context) TraceContext {
	if xrayTraceContext, err := convertXrayTraceContextFromLambdaContext(ctx); err == nil {
		// If there is an active X-Ray segment, use it as the parent
		parentID := xrayTraceContext[parentIDHeader]
		segment := xray.GetSegment(ctx)
		if segment != nil {
			newParentID, err := convertXRayEntityIDToDatadogParentID(segment.ID)
			if err == nil {
				parentID = newParentID
			}
		}

		newTraceContext := map[string]string{}
		newTraceContext[traceIDHeader] = xrayTraceContext[traceIDHeader]
		newTraceContext[samplingPriorityHeader] = xrayTraceContext[samplingPriorityHeader]
		newTraceContext[parentIDHeader] = parentID

		return newTraceContext
	}
	return map[string]string{}
}

// createDummySubsegmentForXrayConverter creates a dummy X-Ray subsegment containing Datadog trace context metadata.
// This metadata is used by the Datadog X-Ray converter to parent the X-Ray trace under the Datadog trace.
// This subsegment will be dropped by the X-Ray converter and will not appear in Datadog.
func createDummySubsegmentForXrayConverter(ctx context.Context, traceCtx TraceContext) error {
	_, segment := xray.BeginSubsegment(ctx, xraySubsegmentName)

	traceID := traceCtx[traceIDHeader]
	parentID := traceCtx[parentIDHeader]
	sampled := traceCtx[samplingPriorityHeader]
	metadata := map[string]string{
		"trace-id":          traceID,
		"parent-id":         parentID,
		"sampling-priority": sampled,
	}

	err := segment.AddMetadataToNamespace(xraySubsegmentNamespace, xraySubsegmentKey, metadata)
	if err != nil {
		return fmt.Errorf("couldn't save trace context to XRay: %v", err)
	}
	segment.Close(nil)
	return nil
}

func getTraceContext(ctx context.Context, headers map[string]string) (TraceContext, bool) {
	tc := TraceContext{}

	traceID := headers[traceIDHeader]
	if traceID == "" {
		if val, ok := ctx.Value(extension.DdTraceId).(string); ok {
			traceID = val
		}
	}
	if traceID == "" {
		return tc, false
	}

	parentID := headers[parentIDHeader]
	if parentID == "" {
		if val, ok := ctx.Value(extension.DdParentId).(string); ok {
			parentID = val
		}
	}
	if parentID == "" {
		return tc, false
	}

	samplingPriority := headers[samplingPriorityHeader]
	if samplingPriority == "" {
		if val, ok := ctx.Value(extension.DdSamplingPriority).(string); ok {
			samplingPriority = val
		}
	}
	if samplingPriority == "" {
		samplingPriority = "1" //sampler-keep
	}

	tc[samplingPriorityHeader] = samplingPriority
	tc[traceIDHeader] = traceID
	tc[parentIDHeader] = parentID

	return tc, true
}

// getHeadersFromEventHeaders extracts the Datadog trace context from an incoming
// Lambda event payload's headers and multivalueHeaders, with headers taking precedence
// then creates a dummy X-Ray subsegment containing this information.
// This is used as the DefaultTraceExtractor.
func getHeadersFromEventHeaders(ctx context.Context, ev json.RawMessage) map[string]string {
	eh := eventWithHeaders{}

	headers := map[string]string{}

	err := json.Unmarshal(ev, &eh)
	if err != nil {
		return headers
	}

	lowercaseHeaders := map[string]string{}

	// extract values from event headers into lowercaseheaders
	for k, v := range eh.Headers {
		lowercaseHeaders[strings.ToLower(k)] = v
	}

	// now extract from multivalue headers
	for k, v := range eh.MultiValueHeaders {
		if len(v) > 0 {
			// If this key was not already extracted from event headers, extract first value from multivalue headers
			if _, ok := lowercaseHeaders[strings.ToLower(k)]; !ok {
				lowercaseHeaders[strings.ToLower(k)] = v[0]
			}
		}
	}

	return lowercaseHeaders
}

func convertXrayTraceContextFromLambdaContext(ctx context.Context) (TraceContext, error) {
	traceCtx := map[string]string{}

	header := getXrayTraceHeaderFromContext(ctx)
	if header == nil {
		return traceCtx, fmt.Errorf("Couldn't read X-Ray trace context from Lambda context object")
	}

	traceID, err := convertXRayTraceIDToDatadogTraceID(header.TraceID)
	if err != nil {
		return traceCtx, fmt.Errorf("Couldn't read trace id from X-Ray: %v", err)
	}
	parentID, err := convertXRayEntityIDToDatadogParentID(header.ParentID)
	if err != nil {
		return traceCtx, fmt.Errorf("Couldn't read parent id from X-Ray: %v", err)
	}
	samplingPriority := convertXRaySamplingDecision(header.SamplingDecision)

	traceCtx[traceIDHeader] = traceID
	traceCtx[parentIDHeader] = parentID
	traceCtx[samplingPriorityHeader] = samplingPriority
	return traceCtx, nil
}

// getXrayTraceHeaderFromContext is used to extract xray segment metadata from the lambda context object.
// By default, the context object won't have any Segment, (xray.GetSegment(ctx) will return nil). However it
// will have the "LambdaTraceHeader" object, which contains the traceID/parentID/sampling info.
func getXrayTraceHeaderFromContext(ctx context.Context) *header.Header {
	var traceHeader string

	if traceHeaderValue := ctx.Value(xray.LambdaTraceHeaderKey); traceHeaderValue != nil {
		traceHeader = traceHeaderValue.(string)
		return header.FromString(traceHeader)
	}
	return nil
}

// Converts the last 63 bits of an X-Ray trace ID (hex) to a Datadog trace id (uint64).
func convertXRayTraceIDToDatadogTraceID(traceID string) (string, error) {
	parts := strings.Split(traceID, "-")

	if len(parts) != 3 {
		return "0", fmt.Errorf("invalid x-ray trace id; expected 3 components in id")
	}
	if len(parts[2]) != 24 {
		return "0", fmt.Errorf("x-ray trace id should be 96 bits")
	}

	traceIDLength := len(parts[2]) - 16
	traceID = parts[2][traceIDLength : traceIDLength+16] // Per XRay Team: use the last 64 bits of the trace id
	apmTraceID, err := convertHexIDToUint64(traceID)
	if err != nil {
		return "0", fmt.Errorf("while converting xray trace id: %v", err)
	}
	apmTraceID = 0x7FFFFFFFFFFFFFFF & apmTraceID // The APM Trace ID is restricted to 63 bits, so make sure the 64th bit is always 0
	return strconv.FormatUint(apmTraceID, 10), nil
}

func convertHexIDToUint64(hexNumber string) (uint64, error) {
	ba, err := hex.DecodeString(hexNumber)
	if err != nil {
		return 0, fmt.Errorf("couldn't convert hex to uint64: %v", err)
	}
	return binary.BigEndian.Uint64(ba), nil // TODO: Verify that this is correct
}

// Converts an X-Ray entity ID (hex) to a Datadog parent id (uint64).
func convertXRayEntityIDToDatadogParentID(entityID string) (string, error) {
	if len(entityID) < 16 {
		return "0", fmt.Errorf("couldn't convert to trace id, too short")
	}
	val, err := convertHexIDToUint64(entityID[len(entityID)-16:])
	if err != nil {
		return "0", fmt.Errorf("couldn't convert entity id to trace id:  %v", err)
	}
	return strconv.FormatUint(val, 10), nil
}

// Converts an X-Ray sampling decision into its Datadog counterpart.
func convertXRaySamplingDecision(decision header.SamplingDecision) string {
	if decision == header.Sampled {
		return userKeep
	}
	return userReject
}

// ConvertTraceContextToSpanContext converts a TraceContext object to a SpanContext that can be used by dd-trace.
func ConvertTraceContextToSpanContext(traceCtx TraceContext) (ddtrace.SpanContext, error) {
	spanCtx, err := propagator.Extract(tracer.TextMapCarrier(traceCtx))

	if err != nil {
		logger.Debug("Could not convert TraceContext to a SpanContext (most likely TraceContext was empty)")
		return nil, err
	}

	return spanCtx, nil
}

// propagator is able to extract a SpanContext object from a TraceContext object
var propagator = tracer.NewPropagator(&tracer.PropagatorConfig{
	TraceHeader:    traceIDHeader,
	ParentHeader:   parentIDHeader,
	PriorityHeader: samplingPriorityHeader,
})
