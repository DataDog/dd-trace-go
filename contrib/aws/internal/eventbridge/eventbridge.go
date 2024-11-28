// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package eventbridge

import (
	"encoding/json"
	"fmt"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/eventbridge"
	"github.com/aws/aws-sdk-go-v2/service/eventbridge/types"
	"github.com/aws/smithy-go/middleware"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"strconv"
	"time"
)

const (
	datadogKey      = "_datadog"
	startTimeKey    = "x-datadog-start-time"
	resourceNameKey = "x-datadog-resource-name"
	maxSizeBytes    = 256 * 1024 // 256 KB
)

func EnrichOperation(span tracer.Span, in middleware.InitializeInput, operation string) {
	switch operation {
	case "PutEvents":
		handlePutEvents(span, in)
	}
}

func handlePutEvents(span tracer.Span, in middleware.InitializeInput) {
	params, ok := in.Parameters.(*eventbridge.PutEventsInput)
	if !ok {
		log.Debug("Unable to read PutEvents params")
		return
	}

	// Create trace context
	carrier := tracer.TextMapCarrier{}
	err := tracer.Inject(span.Context(), carrier)
	if err != nil {
		log.Debug("Unable to inject trace context: %s", err)
		return
	}

	// Add start time
	startTimeMillis := time.Now().UnixMilli()
	carrier[startTimeKey] = strconv.FormatInt(startTimeMillis, 10)

	carrierJSON, err := json.Marshal(carrier)
	if err != nil {
		log.Debug("Unable to marshal trace context: %s", err)
		return
	}

	// Remove last '}'
	reusedTraceContext := string(carrierJSON[:len(carrierJSON)-1])

	for i := range params.Entries {
		injectTraceContext(reusedTraceContext, &params.Entries[i])
	}
}

func injectTraceContext(baseTraceContext string, entryPtr *types.PutEventsRequestEntry) {
	if entryPtr == nil {
		return
	}

	// Build the complete trace context
	var traceContext string
	if entryPtr.EventBusName != nil {
		traceContext = fmt.Sprintf(`%s,"%s":"%s"}`, baseTraceContext, resourceNameKey, *entryPtr.EventBusName)
	} else {
		traceContext = baseTraceContext + "}"
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
		log.Debug("Unable to parse detail JSON. Not injecting trace context into EventBridge payload.")
		return
	}

	// Create new detail string
	var newDetail string
	if len(detail) > 2 {
		// Case where detail is not empty
		newDetail = fmt.Sprintf(`%s,"%s":%s}`, detail[:len(detail)-1], datadogKey, traceContext)
	} else {
		// Cae where detail is empty
		newDetail = fmt.Sprintf(`{"%s":%s}`, datadogKey, traceContext)
	}

	// Check sizes
	if len(newDetail) > maxSizeBytes {
		log.Debug("Payload size too large to pass context")
		return
	}

	entryPtr.Detail = aws.String(newDetail)
}
