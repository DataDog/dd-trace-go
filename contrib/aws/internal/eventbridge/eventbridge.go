// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package eventbridge

import (
	"encoding/json"
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

	for i := range params.Entries {
		injectTraceContext(span, &params.Entries[i])
	}
}

func injectTraceContext(span tracer.Span, entryPtr *types.PutEventsRequestEntry) {
	if entryPtr == nil {
		return
	}

	carrier := tracer.TextMapCarrier{}
	err := tracer.Inject(span.Context(), carrier)
	if err != nil {
		log.Debug("Unable to inject trace context: %s", err)
		return
	}

	// Add start time and resource name
	startTimeMillis := time.Now().UnixMilli()
	carrier[startTimeKey] = strconv.FormatInt(startTimeMillis, 10)
	if entryPtr.EventBusName != nil {
		carrier[resourceNameKey] = *entryPtr.EventBusName
	}

	var detail map[string]interface{}
	if entryPtr.Detail != nil {
		err = json.Unmarshal([]byte(*entryPtr.Detail), &detail)
		if err != nil {
			log.Debug("Unable to unmarshal event detail: %s", err)
			return
		}
	} else {
		detail = make(map[string]interface{})
	}

	jsonBytes, err := json.Marshal(carrier)
	if err != nil {
		log.Debug("Unable to marshal trace context: %s", err)
		return
	}

	detail[datadogKey] = json.RawMessage(jsonBytes)

	updatedDetail, err := json.Marshal(detail)
	if err != nil {
		log.Debug("Unable to marshal modified event detail: %s", err)
		return
	}

	// Check new detail size
	if len(updatedDetail) > maxSizeBytes {
		log.Info("Payload size too large to pass context")
		return
	}

	entryPtr.Detail = aws.String(string(updatedDetail))
}
