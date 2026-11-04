// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sfn

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sfn"
	"github.com/aws/smithy-go/middleware"
	"github.com/stretchr/testify/assert"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
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

	NewEnricher(true).EnrichOperation(context.Background(), span, in, "StartExecution")
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

	NewEnricher(true).EnrichOperation(context.Background(), span, in, "StartSyncExecution")
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

func TestEnrichOperation_InjectionDisabled(t *testing.T) {
	const input = `{"key": "value"}`
	for _, tc := range []struct {
		operation string
		params    func() (any, func() *string)
	}{
		{"StartExecution", func() (any, func() *string) {
			p := &sfn.StartExecutionInput{Input: aws.String(input)}
			return p, func() *string { return p.Input }
		}},
		{"StartSyncExecution", func() (any, func() *string) {
			p := &sfn.StartSyncExecutionInput{Input: aws.String(input)}
			return p, func() *string { return p.Input }
		}},
	} {
		t.Run(tc.operation, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()
			span := tracer.StartSpan("test")

			params, got := tc.params()
			NewEnricher(false).EnrichOperation(context.Background(), span, middleware.InitializeInput{Parameters: params}, tc.operation)
			span.Finish()

			assert.Equal(t, input, *got(), "the input must not change")
			assert.Len(t, mt.FinishedSpans(), 1)
		})
	}
}

// BenchmarkEnrichOperation measures one StartExecution enrichment with the injection on and off.
func BenchmarkEnrichOperation(b *testing.B) {
	mt := mocktracer.Start()
	defer mt.Stop()
	span, ctx := tracer.StartSpanFromContext(context.Background(), "bench")
	defer span.Finish()

	for _, tc := range []struct {
		name   string
		inject bool
	}{{"on", true}, {"off", false}} {
		b.Run(tc.name, func(b *testing.B) {
			e := NewEnricher(tc.inject)
			b.ReportAllocs()
			for b.Loop() {
				in := middleware.InitializeInput{Parameters: &sfn.StartExecutionInput{
					StateMachineArn: aws.String("arn:aws:states:eu-west-1:123456789012:stateMachine:bench"),
					Input:           aws.String(`{"message":"hello"}`),
				}}
				e.EnrichOperation(ctx, span, in, "StartExecution")
			}
		})
	}
}
