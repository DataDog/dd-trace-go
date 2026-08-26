// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package kinesis

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/DataDog/dd-trace-go/contrib/aws/aws-sdk-go-v2/v2/internal"
	"github.com/aws/aws-sdk-go-v2/service/kinesis"
	"github.com/aws/aws-sdk-go-v2/service/kinesis/types"
	"github.com/aws/smithy-go/middleware"

	"github.com/DataDog/dd-trace-go/v2/datastreams"
	"github.com/DataDog/dd-trace-go/v2/datastreams/options"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

const (
	datadogKey         = "_datadog"
	maxRecordSizeBytes = 1024 * 1024
)

var instr = internal.Instr

func EnrichOperation(ctx context.Context, span *tracer.Span, in middleware.InitializeInput, operation string) {
	switch operation {
	case "PutRecord":
		handlePutRecord(ctx, span, in)
	case "PutRecords":
		handlePutRecords(ctx, span, in)
	}
}

func handlePutRecord(ctx context.Context, span *tracer.Span, in middleware.InitializeInput) {
	params, ok := in.Parameters.(*kinesis.PutRecordInput)
	if !ok {
		instr.Logger().Debug("Unable to read PutRecord params")
		return
	}

	carrier, err := getTraceContext(ctx, span, streamName(params.StreamName, params.StreamARN), int64(putRecordSize(params)))
	if err != nil {
		instr.Logger().Debug("Unable to build trace context: %s", err.Error())
		return
	}

	params.Data = injectTraceContext(carrier, params.Data, params.PartitionKey)
}

func handlePutRecords(ctx context.Context, span *tracer.Span, in middleware.InitializeInput) {
	params, ok := in.Parameters.(*kinesis.PutRecordsInput)
	if !ok {
		instr.Logger().Debug("Unable to read PutRecords params")
		return
	}

	stream := streamName(params.StreamName, params.StreamARN)
	for i := range params.Records {
		carrier, err := getTraceContext(ctx, span, stream, int64(putRecordsEntrySize(&params.Records[i])))
		if err != nil {
			instr.Logger().Debug("Unable to build trace context: %s", err.Error())
			continue
		}
		params.Records[i].Data = injectTraceContext(carrier, params.Records[i].Data, params.Records[i].PartitionKey)
	}
}

func getTraceContext(ctx context.Context, span *tracer.Span, stream string, payloadSize int64) (tracer.TextMapCarrier, error) {
	carrier := tracer.TextMapCarrier{}
	if err := tracer.Inject(span.Context(), carrier); err != nil {
		return nil, err
	}

	checkpointCtx, ok := tracer.SetDataStreamsCheckpointWithParams(
		ctx,
		options.CheckpointParams{PayloadSize: payloadSize},
		"direction:out",
		"type:kinesis",
		"topic:"+stream,
	)
	if ok {
		datastreams.InjectToBase64Carrier(checkpointCtx, carrier)
	}

	return carrier, nil
}

func injectTraceContext(carrier tracer.TextMapCarrier, data []byte, partitionKey *string) []byte {
	if len(data) < 2 || data[0] != '{' || data[len(data)-1] != '}' || !json.Valid(data) {
		instr.Logger().Debug("Unable to parse record JSON. Not injecting trace context into Kinesis payload.")
		return data
	}

	traceContextJSON, err := json.Marshal(carrier)
	if err != nil {
		instr.Logger().Debug("Unable to marshal trace context: %s", err.Error())
		return data
	}

	var newData []byte
	if len(data) > 2 {
		newData = []byte(fmt.Sprintf(`%s,"%s":%s}`, data[:len(data)-1], datadogKey, traceContextJSON))
	} else {
		newData = []byte(fmt.Sprintf(`{"%s":%s}`, datadogKey, traceContextJSON))
	}

	if len(newData)+partitionKeySize(partitionKey) > maxRecordSizeBytes {
		instr.Logger().Debug("Record size too large to pass context")
		return data
	}

	return newData
}

func streamName(name *string, arn *string) string {
	if name != nil {
		return *name
	}
	if arn != nil {
		parts := strings.Split(*arn, "/")
		return parts[len(parts)-1]
	}
	return ""
}

func partitionKeySize(partitionKey *string) int {
	if partitionKey == nil {
		return 0
	}
	return len(*partitionKey)
}

func putRecordSize(params *kinesis.PutRecordInput) int {
	if params == nil {
		return 0
	}
	return len(params.Data) + partitionKeySize(params.PartitionKey)
}

func putRecordsEntrySize(entry *types.PutRecordsRequestEntry) int {
	if entry == nil {
		return 0
	}
	return len(entry.Data) + partitionKeySize(entry.PartitionKey)
}
