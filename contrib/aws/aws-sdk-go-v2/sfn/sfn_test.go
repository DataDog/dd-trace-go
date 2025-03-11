// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sfn

import (
	"encoding/json"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sfn"
	"github.com/aws/smithy-go/middleware"
	"github.com/stretchr/testify/assert"
)

func TestEnrichOperation_StartExecution(t *testing.T) {
	mt := mocktracer.Start()
	span := tracer.StartSpan("test")

	params := &sfn.StartExecutionInput{
		Input: aws.String(`{"key": "value"}`),
	}
	in := middleware.InitializeInput{
		Parameters: params,
	}

	EnrichOperation(span, in, "StartExecution")
	span.Finish()
	mt.Stop()

	var inputParsed map[string]interface{}
	err := json.Unmarshal([]byte(*params.Input), &inputParsed)

	assert.Len(t, mt.FinishedSpans(), 1)
	assert.Nil(t, err)
	assert.Equal(t, "value", inputParsed["key"])
	assert.Contains(t, inputParsed, "_datadog")
	assert.Contains(t, inputParsed["_datadog"], "x-datadog-trace-id")
	assert.Contains(t, inputParsed["_datadog"], "x-datadog-parent-id")
}

func TestEnrichOperation_StartSyncExecution(t *testing.T) {
	mt := mocktracer.Start()
	span := tracer.StartSpan("test")

	params := &sfn.StartSyncExecutionInput{
		Input: aws.String(`{"key": "value"}`),
	}
	in := middleware.InitializeInput{
		Parameters: params,
	}

	EnrichOperation(span, in, "StartSyncExecution")
	span.Finish()
	mt.Stop()

	var inputParsed map[string]interface{}
	err := json.Unmarshal([]byte(*params.Input), &inputParsed)

	assert.Len(t, mt.FinishedSpans(), 1)
	assert.Nil(t, err)
	assert.Equal(t, "value", inputParsed["key"])
	assert.Contains(t, inputParsed, "_datadog")
	assert.Contains(t, inputParsed["_datadog"], "x-datadog-trace-id")
	assert.Contains(t, inputParsed["_datadog"], "x-datadog-parent-id")
}
