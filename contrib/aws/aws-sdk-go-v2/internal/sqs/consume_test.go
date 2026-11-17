// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sqs

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/aws/smithy-go/middleware"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/datastreams"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

const testQueueURL = "https://sqs.us-east-1.amazonaws.com/1234567890/consume-queue"

func TestReceiveMessageRequestsDatadogAttribute(t *testing.T) {
	for _, tc := range []struct {
		name        string
		dataStreams bool
		names       []string
		want        []string
	}{
		{"no names", true, nil, []string{datadogKey}},
		{"other names", true, []string{"foo"}, []string{"foo", datadogKey}},
		{"already requested", true, []string{datadogKey}, []string{datadogKey}},
		{"All", true, []string{"All"}, []string{"All"}},
		{"regex all", true, []string{".*"}, []string{".*"}},
		{"data streams off", false, nil, nil},
		{"data streams off with names", false, []string{"foo"}, []string{"foo"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()
			span, ctx := tracer.StartSpanFromContext(context.Background(), "receive")
			params := &sqs.ReceiveMessageInput{QueueUrl: aws.String(testQueueURL), MessageAttributeNames: tc.names}
			original := *params

			out := NewEnricher(true, tc.dataStreams).EnrichOperation(ctx, span, middleware.InitializeInput{Parameters: params}, "ReceiveMessage")

			assert.Equal(t, tc.want, out.Parameters.(*sqs.ReceiveMessageInput).MessageAttributeNames)
			assert.Equal(t, original, *params)
			if len(tc.want) == len(tc.names) {
				assert.Same(t, params, out.Parameters)
			}
		})
	}
}

func TestReceiveMessageDoesNotShareCallerSlice(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()
	span, ctx := tracer.StartSpanFromContext(context.Background(), "receive")
	backing := make([]string, 1, 4)
	backing[0] = "foo"
	params := &sqs.ReceiveMessageInput{QueueUrl: aws.String(testQueueURL), MessageAttributeNames: backing}

	NewEnricher(true, true).EnrichOperation(ctx, span, middleware.InitializeInput{Parameters: params}, "ReceiveMessage")

	assert.Equal(t, "", backing[:2][1])
}

func producerCarrier(t *testing.T, queue string) (context.Context, tracer.TextMapCarrier) {
	t.Helper()
	ctx, ok := tracer.SetDataStreamsCheckpoint(context.Background(), "direction:out", "type:sqs", "topic:"+queue)
	require.True(t, ok)
	carrier := tracer.TextMapCarrier{"x-datadog-trace-id": "123"}
	datastreams.InjectToBase64Carrier(ctx, carrier)
	return ctx, carrier
}

func consumeHash(t *testing.T, parent context.Context, queue string) uint64 {
	t.Helper()
	ctx, ok := tracer.SetDataStreamsCheckpoint(parent, "direction:in", "type:sqs", "topic:"+queue)
	require.True(t, ok)
	pathway, ok := datastreams.PathwayFromContext(ctx)
	require.True(t, ok)
	return pathway.GetHash()
}

func receive(t *testing.T, dataStreams bool, messages ...types.Message) []types.Message {
	t.Helper()
	in := middleware.InitializeInput{Parameters: &sqs.ReceiveMessageInput{QueueUrl: aws.String(testQueueURL)}}
	out := middleware.InitializeOutput{Result: &sqs.ReceiveMessageOutput{Messages: messages}}
	NewEnricher(true, dataStreams).EnrichResponse(in, out, "ReceiveMessage")
	return out.Result.(*sqs.ReceiveMessageOutput).Messages
}

func carrierFromAttribute(t *testing.T, attr types.MessageAttributeValue) tracer.TextMapCarrier {
	t.Helper()
	raw := attr.BinaryValue
	if attr.StringValue != nil {
		raw = []byte(*attr.StringValue)
	}
	var carrier tracer.TextMapCarrier
	require.NoError(t, json.Unmarshal(raw, &carrier))
	return carrier
}

func pathwayHash(t *testing.T, carrier tracer.TextMapCarrier) uint64 {
	t.Helper()
	pathway, ok := datastreams.PathwayFromContext(datastreams.ExtractFromBase64Carrier(context.Background(), carrier))
	require.True(t, ok)
	return pathway.GetHash()
}

func snsBody(t *testing.T, attrType, value string) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"Type":    "Notification",
		"Message": "hello",
		"MessageAttributes": map[string]any{
			datadogKey: map[string]string{"Type": attrType, "Value": value},
		},
	})
	require.NoError(t, err)
	return string(body)
}

func TestReceiveMessageConsumeCheckpoint(t *testing.T) {
	const queue = "consume-queue"
	for _, tc := range []struct {
		name     string
		message  func(carrier []byte) types.Message
		wantType string
	}{
		{
			name: "String attribute",
			message: func(carrier []byte) types.Message {
				return types.Message{Body: aws.String("hello"), MessageAttributes: map[string]types.MessageAttributeValue{
					datadogKey: {DataType: aws.String("String"), StringValue: aws.String(string(carrier))},
				}}
			},
			wantType: "String",
		},
		{
			name: "Binary attribute",
			message: func(carrier []byte) types.Message {
				return types.Message{Body: aws.String("hello"), MessageAttributes: map[string]types.MessageAttributeValue{
					datadogKey: {DataType: aws.String("Binary"), BinaryValue: carrier},
				}}
			},
			wantType: "Binary",
		},
		{
			name: "SNS notification with Binary attribute",
			message: func(carrier []byte) types.Message {
				return types.Message{Body: aws.String(snsBody(t, "Binary", base64.StdEncoding.EncodeToString(carrier)))}
			},
			wantType: "String",
		},
		{
			name: "SNS notification with String attribute",
			message: func(carrier []byte) types.Message {
				return types.Message{Body: aws.String(snsBody(t, "String", string(carrier)))}
			},
			wantType: "String",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()
			producerCtx, carrier := producerCarrier(t, queue)
			raw, err := json.Marshal(carrier)
			require.NoError(t, err)

			got := receive(t, true, tc.message(raw))

			require.Len(t, got, 1)
			attr, ok := got[0].MessageAttributes[datadogKey]
			require.True(t, ok)
			assert.Equal(t, tc.wantType, aws.ToString(attr.DataType))
			written := carrierFromAttribute(t, attr)
			assert.Equal(t, "123", written["x-datadog-trace-id"])
			want := consumeHash(t, producerCtx, queue)
			assert.Equal(t, want, pathwayHash(t, written))
			assert.NotEqual(t, consumeHash(t, context.Background(), queue), want)
		})
	}
}

func TestReceiveMessageWithoutContext(t *testing.T) {
	const queue = "consume-queue"
	for _, tc := range []struct {
		name string
		body string
	}{
		{"plain body", "hello"},
		{"JSON body", `{"_datadog":"not a notification"}`},
		{"malformed JSON", `{"Type":"Notification","_datadog"`},
		{"notification without attribute", `{"Type":"Notification","Message":"_datadog"}`},
		{"notification with bad base64", snsBody(t, "Binary", "!!")},
		{"notification with unknown type", snsBody(t, "Number", "1")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()

			got := receive(t, true, types.Message{Body: aws.String(tc.body)})

			require.Len(t, got, 1)
			assert.Equal(t, consumeHash(t, context.Background(), queue), pathwayHash(t, carrierFromAttribute(t, got[0].MessageAttributes[datadogKey])))
		})
	}
}

func TestReceiveMessageMalformedAttribute(t *testing.T) {
	const queue = "consume-queue"
	mt := mocktracer.Start()
	defer mt.Stop()

	got := receive(t, true,
		types.Message{MessageAttributes: map[string]types.MessageAttributeValue{datadogKey: {DataType: aws.String("String"), StringValue: aws.String("{")}}},
		types.Message{MessageAttributes: map[string]types.MessageAttributeValue{datadogKey: {DataType: aws.String("Number")}}},
	)

	require.Len(t, got, 2)
	root := consumeHash(t, context.Background(), queue)
	for _, msg := range got {
		assert.Equal(t, root, pathwayHash(t, carrierFromAttribute(t, msg.MessageAttributes[datadogKey])))
	}
}

func TestReceiveMessageDataStreamsOff(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()
	_, carrier := producerCarrier(t, "consume-queue")
	raw, err := json.Marshal(carrier)
	require.NoError(t, err)
	attrs := map[string]types.MessageAttributeValue{datadogKey: {DataType: aws.String("String"), StringValue: aws.String(string(raw))}}

	got := receive(t, false, types.Message{Body: aws.String("hello")}, types.Message{Body: aws.String("hello"), MessageAttributes: attrs})

	assert.Nil(t, got[0].MessageAttributes)
	assert.Equal(t, string(raw), aws.ToString(got[1].MessageAttributes[datadogKey].StringValue))
}

func TestEnrichResponseIgnoresOtherResults(t *testing.T) {
	e := NewEnricher(true, true)
	receiveIn := middleware.InitializeInput{Parameters: &sqs.ReceiveMessageInput{QueueUrl: aws.String(testQueueURL)}}
	assert.NotPanics(t, func() {
		e.EnrichResponse(receiveIn, middleware.InitializeOutput{Result: &sqs.SendMessageOutput{}}, "ReceiveMessage")
		e.EnrichResponse(receiveIn, middleware.InitializeOutput{Result: (*sqs.ReceiveMessageOutput)(nil)}, "ReceiveMessage")
		e.EnrichResponse(middleware.InitializeInput{Parameters: &sqs.SendMessageInput{}}, middleware.InitializeOutput{Result: &sqs.ReceiveMessageOutput{}}, "ReceiveMessage")
		e.EnrichResponse(receiveIn, middleware.InitializeOutput{Result: &sqs.ReceiveMessageOutput{}}, "SendMessage")
	})
}

func TestProduceDataStreamsOff(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()
	span, ctx := tracer.StartSpanFromContext(context.Background(), "produce")

	single := NewEnricher(true, false).EnrichOperation(ctx, span, middleware.InitializeInput{Parameters: &sqs.SendMessageInput{QueueUrl: aws.String(testQueueURL), MessageBody: aws.String("a")}}, "SendMessage")
	batch := NewEnricher(true, false).EnrichOperation(ctx, span, middleware.InitializeInput{Parameters: &sqs.SendMessageBatchInput{QueueUrl: aws.String(testQueueURL), Entries: []types.SendMessageBatchRequestEntry{{Id: aws.String("1"), MessageBody: aws.String("a")}}}}, "SendMessageBatch")

	for _, attrs := range []map[string]types.MessageAttributeValue{
		single.Parameters.(*sqs.SendMessageInput).MessageAttributes,
		batch.Parameters.(*sqs.SendMessageBatchInput).Entries[0].MessageAttributes,
	} {
		carrier := carrierFromAttribute(t, attrs[datadogKey])
		assert.NotContains(t, carrier, pathwayContextKey)
		assert.Contains(t, carrier, "x-datadog-trace-id")
	}
}

func BenchmarkEnrichResponse(b *testing.B) {
	mt := mocktracer.Start()
	defer mt.Stop()
	ctx, _ := tracer.SetDataStreamsCheckpoint(context.Background(), "direction:out", "type:sqs", "topic:bench")
	carrier := tracer.TextMapCarrier{}
	datastreams.InjectToBase64Carrier(ctx, carrier)
	raw, _ := json.Marshal(carrier)
	in := middleware.InitializeInput{Parameters: &sqs.ReceiveMessageInput{QueueUrl: aws.String(testQueueURL)}}
	e := NewEnricher(true, true)

	for _, tc := range []struct {
		name    string
		message func() types.Message
	}{
		{"attribute", func() types.Message {
			return types.Message{Body: aws.String(`{"message":"hello"}`), MessageAttributes: map[string]types.MessageAttributeValue{
				datadogKey: {DataType: aws.String("String"), StringValue: aws.String(string(raw))},
			}}
		}},
		{"sns notification", func() types.Message {
			body, _ := json.Marshal(map[string]any{"Type": "Notification", "Message": `{"message":"hello"}`, "MessageAttributes": map[string]any{
				datadogKey: map[string]string{"Type": "Binary", "Value": base64.StdEncoding.EncodeToString(raw)},
			}})
			return types.Message{Body: aws.String(string(body))}
		}},
		{"no context", func() types.Message { return types.Message{Body: aws.String(`{"message":"hello"}`)} }},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				out := middleware.InitializeOutput{Result: &sqs.ReceiveMessageOutput{Messages: []types.Message{tc.message()}}}
				e.EnrichResponse(in, out, "ReceiveMessage")
			}
		})
	}
}
