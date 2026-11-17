// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package aws

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/datastreams"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

const dsmQueueURL = "https://sqs.eu-west-1.amazonaws.com/123456789012/dsm-queue"

type sqsCall struct {
	target string
	body   []byte
}

func sqsServer(calls *[]sqsCall, response string) aws.HTTPClient {
	return smithyhttp.ClientDoFunc(func(req *http.Request) (*http.Response, error) {
		defer req.Body.Close()
		body, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		*calls = append(*calls, sqsCall{target: req.Header.Get("X-Amz-Target"), body: body})
		body = []byte(`{}`)
		if strings.HasSuffix(req.Header.Get("X-Amz-Target"), "ReceiveMessage") {
			body = []byte(response)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
	})
}

func dsmClient(calls *[]sqsCall, response string, opts ...Option) *sqs.Client {
	awsCfg := aws.Config{
		Region:      "eu-west-1",
		Credentials: aws.AnonymousCredentials{},
		HTTPClient:  sqsServer(calls, response),
	}
	AppendMiddleware(&awsCfg, opts...)
	return sqs.NewFromConfig(awsCfg)
}

func sentCarriers(t *testing.T, calls []sqsCall) []tracer.TextMapCarrier {
	t.Helper()
	var carriers []tracer.TextMapCarrier
	for _, call := range calls {
		var request struct {
			MessageAttributes map[string]sqstypes.MessageAttributeValue
			Entries           []struct {
				MessageAttributes map[string]sqstypes.MessageAttributeValue
			}
		}
		require.NoError(t, json.Unmarshal(call.body, &request))
		attrs := make([]map[string]sqstypes.MessageAttributeValue, 0, 1+len(request.Entries))
		attrs = append(attrs, request.MessageAttributes)
		for _, entry := range request.Entries {
			attrs = append(attrs, entry.MessageAttributes)
		}
		for _, a := range attrs {
			if a == nil {
				continue
			}
			var carrier tracer.TextMapCarrier
			require.NoError(t, json.Unmarshal([]byte(aws.ToString(a["_datadog"].StringValue)), &carrier))
			carriers = append(carriers, carrier)
		}
	}
	return carriers
}

func hashOf(t *testing.T, ctx context.Context) uint64 {
	t.Helper()
	pathway, ok := datastreams.PathwayFromContext(ctx)
	require.True(t, ok)
	return pathway.GetHash()
}

func produce(t *testing.T, client *sqs.Client) {
	t.Helper()
	_, err := client.SendMessage(context.Background(), &sqs.SendMessageInput{QueueUrl: aws.String(dsmQueueURL), MessageBody: aws.String("one")})
	require.NoError(t, err)
	_, err = client.SendMessageBatch(context.Background(), &sqs.SendMessageBatchInput{QueueUrl: aws.String(dsmQueueURL), Entries: []sqstypes.SendMessageBatchRequestEntry{
		{Id: aws.String("1"), MessageBody: aws.String("two")},
		{Id: aws.String("2"), MessageBody: aws.String("three")},
	}})
	require.NoError(t, err)
}

func TestSQSDataStreamsProduce(t *testing.T) {
	for _, tc := range []struct {
		name        string
		env         string
		opts        []Option
		wantPathway bool
	}{
		{"env var", "true", nil, true},
		{"WithDataStreams", "", []Option{WithDataStreams()}, true},
		{"off", "", nil, false},
		{"env var false", "false", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("DD_DATA_STREAMS_ENABLED", tc.env)
			mt := mocktracer.Start()
			defer mt.Stop()
			var calls []sqsCall

			produce(t, dsmClient(&calls, `{}`, tc.opts...))

			carriers := sentCarriers(t, calls)
			require.Len(t, carriers, 3)
			rootCtx, ok := tracer.SetDataStreamsCheckpoint(context.Background(), "direction:out", "type:sqs", "topic:dsm-queue")
			require.True(t, ok)
			for _, carrier := range carriers {
				assert.Contains(t, carrier, "x-datadog-trace-id")
				if !tc.wantPathway {
					assert.NotContains(t, carrier, "dd-pathway-ctx-base64")
					continue
				}
				require.Contains(t, carrier, "dd-pathway-ctx-base64")
				assert.Equal(t, hashOf(t, rootCtx), hashOf(t, datastreams.ExtractFromBase64Carrier(context.Background(), carrier)))
			}
		})
	}
}

func receiveResponse(t *testing.T, producer tracer.TextMapCarrier) string {
	t.Helper()
	raw, err := json.Marshal(producer)
	require.NoError(t, err)
	notification, err := json.Marshal(map[string]any{
		"Type":    "Notification",
		"Message": "four",
		"MessageAttributes": map[string]any{
			"_datadog": map[string]string{"Type": "Binary", "Value": base64.StdEncoding.EncodeToString(raw)},
		},
	})
	require.NoError(t, err)
	response, err := json.Marshal(map[string]any{"Messages": []map[string]any{
		{"MessageId": "1", "Body": "one", "MessageAttributes": map[string]any{"_datadog": map[string]string{"DataType": "String", "StringValue": string(raw)}}},
		{"MessageId": "2", "Body": "two", "MessageAttributes": map[string]any{"_datadog": map[string]any{"DataType": "Binary", "BinaryValue": raw}}},
		{"MessageId": "3", "Body": string(notification)},
	}})
	require.NoError(t, err)
	return string(response)
}

func TestSQSDataStreamsConsume(t *testing.T) {
	for _, tc := range []struct {
		name        string
		env         string
		opts        []Option
		names       []string
		wantNames   []string
		wantPathway bool
	}{
		{"env var", "true", nil, nil, []string{"_datadog"}, true},
		{"env var with other names", "true", nil, []string{"foo"}, []string{"foo", "_datadog"}, true},
		{"env var with All", "true", nil, []string{"All"}, []string{"All"}, true},
		{"WithDataStreams", "", []Option{WithDataStreams()}, nil, []string{"_datadog"}, true},
		{"off", "", nil, nil, nil, false},
		{"off with names", "", nil, []string{"foo"}, []string{"foo"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("DD_DATA_STREAMS_ENABLED", tc.env)
			mt := mocktracer.Start()
			defer mt.Stop()
			producerCtx, ok := tracer.SetDataStreamsCheckpoint(context.Background(), "direction:out", "type:sqs", "topic:dsm-queue")
			require.True(t, ok)
			producer := tracer.TextMapCarrier{}
			datastreams.InjectToBase64Carrier(producerCtx, producer)
			var calls []sqsCall
			input := &sqs.ReceiveMessageInput{QueueUrl: aws.String(dsmQueueURL), MessageAttributeNames: tc.names}

			out, err := dsmClient(&calls, receiveResponse(t, producer), tc.opts...).ReceiveMessage(context.Background(), input)
			require.NoError(t, err)

			require.Len(t, calls, 1)
			var request sqs.ReceiveMessageInput
			require.NoError(t, json.Unmarshal(calls[0].body, &request))
			assert.Equal(t, tc.wantNames, request.MessageAttributeNames)
			assert.Equal(t, tc.names, input.MessageAttributeNames)

			require.Len(t, out.Messages, 3)
			if !tc.wantPathway {
				assert.Nil(t, out.Messages[2].MessageAttributes)
				assert.Equal(t, producer["dd-pathway-ctx-base64"], decodeAttribute(t, out.Messages[0].MessageAttributes["_datadog"])["dd-pathway-ctx-base64"])
				return
			}
			consumeCtx, ok := tracer.SetDataStreamsCheckpoint(producerCtx, "direction:in", "type:sqs", "topic:dsm-queue")
			require.True(t, ok)
			for _, msg := range out.Messages {
				carrier := decodeAttribute(t, msg.MessageAttributes["_datadog"])
				assert.Equal(t, hashOf(t, consumeCtx), hashOf(t, datastreams.ExtractFromBase64Carrier(context.Background(), carrier)), aws.ToString(msg.MessageId))
			}
		})
	}
}

func decodeAttribute(t *testing.T, attr sqstypes.MessageAttributeValue) tracer.TextMapCarrier {
	t.Helper()
	raw := attr.BinaryValue
	if attr.StringValue != nil {
		raw = []byte(*attr.StringValue)
	}
	var carrier tracer.TextMapCarrier
	require.NoError(t, json.Unmarshal(raw, &carrier))
	return carrier
}

func TestSQSDataStreamsPathwayAcrossServices(t *testing.T) {
	t.Setenv("DD_DATA_STREAMS_ENABLED", "true")
	mt := mocktracer.Start()
	defer mt.Stop()
	var produced []sqsCall
	_, err := dsmClient(&produced, `{}`).SendMessage(context.Background(), &sqs.SendMessageInput{QueueUrl: aws.String(dsmQueueURL), MessageBody: aws.String("one")})
	require.NoError(t, err)
	sent := sentCarriers(t, produced)
	require.Len(t, sent, 1)

	var consumed []sqsCall
	out, err := dsmClient(&consumed, receiveResponse(t, sent[0])).ReceiveMessage(context.Background(), &sqs.ReceiveMessageInput{QueueUrl: aws.String(dsmQueueURL)})
	require.NoError(t, err)

	produceCtx, _ := tracer.SetDataStreamsCheckpoint(context.Background(), "direction:out", "type:sqs", "topic:dsm-queue")
	consumeCtx, _ := tracer.SetDataStreamsCheckpoint(produceCtx, "direction:in", "type:sqs", "topic:dsm-queue")
	carrier := decodeAttribute(t, out.Messages[0].MessageAttributes["_datadog"])
	assert.Equal(t, sent[0]["x-datadog-trace-id"], carrier["x-datadog-trace-id"])
	assert.Equal(t, hashOf(t, consumeCtx), hashOf(t, datastreams.ExtractFromBase64Carrier(context.Background(), carrier)))
}
