// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package aws

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/eventbridge"
	ebtypes "github.com/aws/aws-sdk-go-v2/service/eventbridge/types"
	"github.com/aws/aws-sdk-go-v2/service/kinesis"
	kinesistypes "github.com/aws/aws-sdk-go-v2/service/kinesis/types"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	snstypes "github.com/aws/aws-sdk-go-v2/service/sns/types"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

// injectOp is one request type of a service.
type injectOp struct {
	operation string // matches the "aws.operation" span tag
	entries   int    // number of messages in the request, each one gets its own checkpoint
	// run sends the request and reports, for each entry, whether the trace context was written into it.
	run func(ctx context.Context, cfg aws.Config) []bool
}

type injectCase struct {
	service string // matches the "aws.service" span tag
	env     string // environment variable that disables injection
	ops     []injectOp
}

func injectCases() []injectCase {
	return []injectCase{
		{
			service: "Kinesis", env: "DD_TRACE_KINESIS_INJECT_DATADOG_ATTRIBUTE_ENABLED",
			ops: []injectOp{
				{operation: "PutRecord", entries: 1, run: func(ctx context.Context, cfg aws.Config) []bool {
					in := &kinesis.PutRecordInput{
						StreamName:   aws.String("my-stream"),
						PartitionKey: aws.String("pk"),
						Data:         []byte(`{"message":"hello"}`),
					}
					_, _ = kinesis.NewFromConfig(cfg).PutRecord(ctx, in)
					return []bool{strings.Contains(string(in.Data), "_datadog")}
				}},
				{operation: "PutRecords", entries: 2, run: func(ctx context.Context, cfg aws.Config) []bool {
					in := &kinesis.PutRecordsInput{
						StreamName: aws.String("my-stream"),
						Records: []kinesistypes.PutRecordsRequestEntry{
							{PartitionKey: aws.String("pk1"), Data: []byte(`{"message":"hello"}`)},
							{PartitionKey: aws.String("pk2"), Data: []byte(`{"message":"a longer message"}`)},
						},
					}
					_, _ = kinesis.NewFromConfig(cfg).PutRecords(ctx, in)
					injected := make([]bool, len(in.Records))
					for i, r := range in.Records {
						injected[i] = strings.Contains(string(r.Data), "_datadog")
					}
					return injected
				}},
			},
		},
		{
			service: "EventBridge", env: "DD_TRACE_EVENTBRIDGE_INJECT_DATADOG_ATTRIBUTE_ENABLED",
			ops: []injectOp{
				{operation: "PutEvents", entries: 2, run: func(ctx context.Context, cfg aws.Config) []bool {
					entry := func(detail string) ebtypes.PutEventsRequestEntry {
						return ebtypes.PutEventsRequestEntry{
							EventBusName: aws.String("my-bus"),
							Source:       aws.String("test"),
							DetailType:   aws.String("test"),
							Detail:       aws.String(detail),
						}
					}
					in := &eventbridge.PutEventsInput{Entries: []ebtypes.PutEventsRequestEntry{
						entry(`{"message":"hello"}`), entry(`{"message":"a longer message"}`),
					}}
					_, _ = eventbridge.NewFromConfig(cfg).PutEvents(ctx, in)
					injected := make([]bool, len(in.Entries))
					for i, e := range in.Entries {
						injected[i] = strings.Contains(aws.ToString(e.Detail), "_datadog")
					}
					return injected
				}},
			},
		},
		{
			service: "SQS", env: "DD_TRACE_SQS_INJECT_DATADOG_ATTRIBUTE_ENABLED",
			ops: []injectOp{
				{operation: "SendMessage", entries: 1, run: func(ctx context.Context, cfg aws.Config) []bool {
					in := &sqs.SendMessageInput{
						QueueUrl:    aws.String("https://sqs.eu-west-1.amazonaws.com/123456789012/my-queue"),
						MessageBody: aws.String(`{"message":"hello"}`),
					}
					_, _ = sqs.NewFromConfig(cfg).SendMessage(ctx, in)
					_, ok := in.MessageAttributes["_datadog"]
					return []bool{ok}
				}},
				{operation: "SendMessageBatch", entries: 2, run: func(ctx context.Context, cfg aws.Config) []bool {
					in := &sqs.SendMessageBatchInput{
						QueueUrl: aws.String("https://sqs.eu-west-1.amazonaws.com/123456789012/my-queue"),
						Entries: []sqstypes.SendMessageBatchRequestEntry{
							{Id: aws.String("1"), MessageBody: aws.String(`{"message":"hello"}`)},
							{Id: aws.String("2"), MessageBody: aws.String(`{"message":"a longer message"}`)},
						},
					}
					_, _ = sqs.NewFromConfig(cfg).SendMessageBatch(ctx, in)
					injected := make([]bool, len(in.Entries))
					for i, e := range in.Entries {
						_, injected[i] = e.MessageAttributes["_datadog"]
					}
					return injected
				}},
			},
		},
		{
			service: "SNS", env: "DD_TRACE_SNS_INJECT_DATADOG_ATTRIBUTE_ENABLED",
			ops: []injectOp{
				{operation: "Publish", entries: 1, run: func(ctx context.Context, cfg aws.Config) []bool {
					in := &sns.PublishInput{
						TopicArn: aws.String("arn:aws:sns:eu-west-1:123456789012:my-topic"),
						Message:  aws.String(`{"message":"hello"}`),
					}
					_, _ = sns.NewFromConfig(cfg).Publish(ctx, in)
					_, ok := in.MessageAttributes["_datadog"]
					return []bool{ok}
				}},
				{operation: "PublishBatch", entries: 2, run: func(ctx context.Context, cfg aws.Config) []bool {
					in := &sns.PublishBatchInput{
						TopicArn: aws.String("arn:aws:sns:eu-west-1:123456789012:my-topic"),
						PublishBatchRequestEntries: []snstypes.PublishBatchRequestEntry{
							{Id: aws.String("1"), Message: aws.String(`{"message":"hello"}`)},
							{Id: aws.String("2"), Message: aws.String(`{"message":"a longer message"}`)},
						},
					}
					_, _ = sns.NewFromConfig(cfg).PublishBatch(ctx, in)
					injected := make([]bool, len(in.PublishBatchRequestEntries))
					for i, e := range in.PublishBatchRequestEntries {
						_, injected[i] = e.MessageAttributes["_datadog"]
					}
					return injected
				}},
			},
		},
	}
}

// reloadTracerConfig makes the global configuration re-read the environment.
func reloadTracerConfig(t *testing.T) {
	t.Helper()
	require.NoError(t, tracer.Start(tracer.WithTraceEnabled(false)))
	tracer.Stop()
}

// clearInjectEnv unsets the four injection switches so that a value inherited from the environment
// of the test process cannot change the result, then reloads the global configuration after the
// original environment is restored.
func clearInjectEnv(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { reloadTracerConfig(t) }) // Runs after t.Setenv has restored the variables.
	for _, tc := range injectCases() {
		t.Setenv(tc.env, "") // Restores the original value at the end of the test.
		require.NoError(t, os.Unsetenv(tc.env))
	}
}

// setInjectEnv unsets all the injection switches, sets one of them and reloads the global
// configuration.
func setInjectEnv(t *testing.T, key, value string) {
	t.Helper()
	clearInjectEnv(t)
	t.Setenv(key, value)
	reloadTracerConfig(t)
}

func newInjectTestConfig(t *testing.T) aws.Config {
	t.Helper()
	var wire wireRecorder
	server := mockAWSRecording(200, &wire)
	t.Cleanup(server.Close)

	resolver := aws.EndpointResolverFunc(func(_, _ string) (aws.Endpoint, error) {
		return aws.Endpoint{PartitionID: "aws", URL: server.URL, SigningRegion: "eu-west-1"}, nil
	})
	awsCfg := aws.Config{
		Region:           "eu-west-1",
		Credentials:      aws.AnonymousCredentials{},
		EndpointResolver: resolver,
	}
	AppendMiddleware(&awsCfg)
	return awsCfg
}

func TestInjectionEnabledByDefault(t *testing.T) {
	for _, tc := range injectCases() {
		for _, op := range tc.ops {
			t.Run(tc.service+"/"+op.operation, func(t *testing.T) {
				clearInjectEnv(t)
				reloadTracerConfig(t)
				mt := mocktracer.Start()
				defer mt.Stop()

				injected := op.run(context.Background(), newInjectTestConfig(t))
				require.Len(t, injected, op.entries)
				for i, ok := range injected {
					assert.True(t, ok, "entry %d", i)
				}
			})
		}
	}
}

func TestInjectionDisabledPerService(t *testing.T) {
	cases := injectCases()
	for _, off := range cases {
		t.Run(off.service, func(t *testing.T) {
			setInjectEnv(t, off.env, "false")

			for _, tc := range cases {
				for _, op := range tc.ops {
					mt := mocktracer.Start()
					injected := op.run(context.Background(), newInjectTestConfig(t))
					spans := mt.FinishedSpans()
					mt.Stop()

					// Every entry of a batch request is checked, so that a missing guard in the loop is caught.
					require.Len(t, injected, op.entries, "%s %s", tc.service, op.operation)
					for i, ok := range injected {
						assert.Equal(t, tc.service != off.service, ok,
							"%s %s entry %d with only %s disabled", tc.service, op.operation, i, off.service)
					}

					require.Len(t, spans, 1, "%s %s", tc.service, op.operation)
					assert.Equal(t, op.operation, spans[0].Tag("aws.operation"), tc.service)
					assert.Equal(t, tc.service, spans[0].Tag("aws.service"), tc.service)
					if tc.service == "SQS" {
						assert.Equal(t, ext.MessagingSystemSQS, spans[0].Tag(ext.MessagingSystem))
					}
				}
			}
		})
	}
}

func TestInjectionEnvFalseyAndTruthyValues(t *testing.T) {
	kinesisCase := injectCases()[0]
	for value, want := range map[string]bool{"false": false, "0": false, "true": true, "1": true} {
		t.Run(value, func(t *testing.T) {
			setInjectEnv(t, kinesisCase.env, value)
			mt := mocktracer.Start()
			defer mt.Stop()
			assert.Equal(t, []bool{want}, kinesisCase.ops[0].run(context.Background(), newInjectTestConfig(t)))
		})
	}
}
