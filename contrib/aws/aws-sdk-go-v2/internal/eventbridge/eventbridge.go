// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package eventbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/DataDog/dd-trace-go/contrib/aws/aws-sdk-go-v2/v2/internal"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/eventbridge"
	"github.com/aws/aws-sdk-go-v2/service/eventbridge/types"
	"github.com/aws/smithy-go/middleware"

	"github.com/DataDog/dd-trace-go/v2/datastreams"
	"github.com/DataDog/dd-trace-go/v2/datastreams/options"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

const (
	datadogKey      = "_datadog"
	startTimeKey    = "x-datadog-start-time"
	resourceNameKey = "x-datadog-resource-name"
	maxSizeBytes    = 1024 * 1024 // 1 MB
)

var instr = internal.Instr

func EnrichOperation(ctx context.Context, span *tracer.Span, in middleware.InitializeInput, operation string, dsmEnabled bool) {
	switch operation {
	case "PutEvents":
		handlePutEvents(ctx, span, in, dsmEnabled)
	}
}

func handlePutEvents(ctx context.Context, span *tracer.Span, in middleware.InitializeInput, dsmEnabled bool) {
	params, ok := in.Parameters.(*eventbridge.PutEventsInput)
	if !ok {
		instr.Logger().Debug("Unable to read PutEvents params")
		return
	}

	startTimeMillis := time.Now().UnixMilli()

	for i := range params.Entries {
		carrier, err := getTraceContext(ctx, span, &params.Entries[i], startTimeMillis, dsmEnabled)
		if err != nil {
			instr.Logger().Debug("Unable to build trace context: %s", err.Error())
			continue
		}
		injectTraceContext(carrier, &params.Entries[i])
	}
}

func getTraceContext(ctx context.Context, span *tracer.Span, entry *types.PutEventsRequestEntry, startTimeMillis int64, dsmEnabled bool) (tracer.TextMapCarrier, error) {
	carrier := tracer.TextMapCarrier{}
	if err := tracer.Inject(span.Context(), carrier); err != nil {
		return nil, err
	}

	if dsmEnabled {
		checkpointCtx, ok := tracer.SetDataStreamsCheckpointWithParams(
			ctx,
			options.CheckpointParams{PayloadSize: payloadSize(entry)},
			eventBridgeEdgeTags(entry)...,
		)
		if ok {
			datastreams.InjectToBase64Carrier(checkpointCtx, carrier)
		}
	}

	carrier[startTimeKey] = strconv.FormatInt(startTimeMillis, 10)
	if entry != nil && entry.EventBusName != nil {
		carrier[resourceNameKey] = *entry.EventBusName
	}
	return carrier, nil
}

func eventBridgeEdgeTags(entry *types.PutEventsRequestEntry) []string {
	return []string{
		"direction:out",
		"exchange:" + eventBusName(entry),
		"topic:" + detailType(entry),
		"type:eventbridge",
	}
}

func eventBusName(entry *types.PutEventsRequestEntry) string {
	if entry == nil || entry.EventBusName == nil || *entry.EventBusName == "" {
		return "default"
	}
	return *entry.EventBusName
}

func detailType(entry *types.PutEventsRequestEntry) string {
	if entry == nil || entry.DetailType == nil {
		return "unknown"
	}
	return *entry.DetailType
}

func payloadSize(entry *types.PutEventsRequestEntry) int64 {
	if entry == nil {
		return 0
	}

	var size int64
	if entry.Detail != nil {
		size += int64(len(*entry.Detail))
	}
	if entry.DetailType != nil {
		size += int64(len(*entry.DetailType))
	}
	if entry.EventBusName != nil {
		size += int64(len(*entry.EventBusName))
	}
	for _, resource := range entry.Resources {
		size += int64(len(resource))
	}
	if entry.Source != nil {
		size += int64(len(*entry.Source))
	}
	if entry.TraceHeader != nil {
		size += int64(len(*entry.TraceHeader))
	}
	return size
}

func injectTraceContext(carrier tracer.TextMapCarrier, entryPtr *types.PutEventsRequestEntry) {
	if entryPtr == nil {
		return
	}

	traceContextJSON, err := json.Marshal(carrier)
	if err != nil {
		instr.Logger().Debug("Unable to marshal trace context: %s", err.Error())
		return
	}

	// Get current detail string
	var detail string
	if entryPtr.Detail == nil || *entryPtr.Detail == "" {
		detail = "{}"
	} else {
		detail = *entryPtr.Detail
	}

	// Basic JSON structure validation
	if len(detail) < 2 || detail[len(detail)-1] != '}' {
		instr.Logger().Debug("Unable to parse detail JSON. Not injecting trace context into EventBridge payload.")
		return
	}

	// Create new detail string
	var newDetail string
	if len(detail) > 2 {
		// Case where detail is not empty
		newDetail = fmt.Sprintf(`%s,"%s":%s}`, detail[:len(detail)-1], datadogKey, traceContextJSON)
	} else {
		// Cae where detail is empty
		newDetail = fmt.Sprintf(`{"%s":%s}`, datadogKey, traceContextJSON)
	}

	// Check sizes
	if len(newDetail) > maxSizeBytes {
		instr.Logger().Debug("Payload size too large to pass context")
		return
	}

	entryPtr.Detail = aws.String(newDetail)
}
