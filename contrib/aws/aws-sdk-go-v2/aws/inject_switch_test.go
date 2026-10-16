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
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

type injectCase struct {
	service   string // matches the "aws.service" span tag
	operation string // matches the "aws.operation" span tag
	env       string // environment variable that disables injection
	run       func(ctx context.Context, cfg aws.Config) (injected bool)
}

func injectCases() []injectCase {
	return []injectCase{
		{
			service: "Kinesis", operation: "PutRecord", env: "DD_TRACE_KINESIS_INJECT_DATADOG_ATTRIBUTE_ENABLED",
			run: func(ctx context.Context, cfg aws.Config) bool {
				in := &kinesis.PutRecordInput{
					StreamName:   aws.String("my-stream"),
					PartitionKey: aws.String("pk"),
					Data:         []byte(`{"message":"hello"}`),
				}
				_, _ = kinesis.NewFromConfig(cfg).PutRecord(ctx, in)
				return strings.Contains(string(in.Data), "_datadog")
			},
		},
		{
			service: "EventBridge", operation: "PutEvents", env: "DD_TRACE_EVENTBRIDGE_INJECT_DATADOG_ATTRIBUTE_ENABLED",
			run: func(ctx context.Context, cfg aws.Config) bool {
				in := &eventbridge.PutEventsInput{Entries: []ebtypes.PutEventsRequestEntry{{
					EventBusName: aws.String("my-bus"),
					Source:       aws.String("test"),
					DetailType:   aws.String("test"),
					Detail:       aws.String(`{"message":"hello"}`),
				}}}
				_, _ = eventbridge.NewFromConfig(cfg).PutEvents(ctx, in)
				return strings.Contains(aws.ToString(in.Entries[0].Detail), "_datadog")
			},
		},
		{
			service: "SQS", operation: "SendMessage", env: "DD_TRACE_SQS_INJECT_DATADOG_ATTRIBUTE_ENABLED",
			run: func(ctx context.Context, cfg aws.Config) bool {
				in := &sqs.SendMessageInput{
					QueueUrl:    aws.String("https://sqs.eu-west-1.amazonaws.com/123456789012/my-queue"),
					MessageBody: aws.String(`{"message":"hello"}`),
				}
				_, _ = sqs.NewFromConfig(cfg).SendMessage(ctx, in)
				_, ok := in.MessageAttributes["_datadog"]
				return ok
			},
		},
		{
			service: "SNS", operation: "Publish", env: "DD_TRACE_SNS_INJECT_DATADOG_ATTRIBUTE_ENABLED",
			run: func(ctx context.Context, cfg aws.Config) bool {
				in := &sns.PublishInput{
					TopicArn: aws.String("arn:aws:sns:eu-west-1:123456789012:my-topic"),
					Message:  aws.String(`{"message":"hello"}`),
				}
				_, _ = sns.NewFromConfig(cfg).Publish(ctx, in)
				_, ok := in.MessageAttributes["_datadog"]
				return ok
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
		t.Run(tc.service, func(t *testing.T) {
			clearInjectEnv(t)
			reloadTracerConfig(t)
			mt := mocktracer.Start()
			defer mt.Stop()

			assert.True(t, tc.run(context.Background(), newInjectTestConfig(t)))
		})
	}
}

func TestInjectionDisabledPerService(t *testing.T) {
	cases := injectCases()
	for _, off := range cases {
		t.Run(off.service, func(t *testing.T) {
			setInjectEnv(t, off.env, "false")

			for _, tc := range cases {
				mt := mocktracer.Start()
				injected := tc.run(context.Background(), newInjectTestConfig(t))
				spans := mt.FinishedSpans()
				mt.Stop()

				assert.Equal(t, tc.service != off.service, injected,
					"%s injection with only %s disabled", tc.service, off.service)

				require.Len(t, spans, 1, tc.service)
				assert.Equal(t, tc.operation, spans[0].Tag("aws.operation"), tc.service)
				assert.Equal(t, tc.service, spans[0].Tag("aws.service"), tc.service)
				if tc.service == "SQS" {
					assert.Equal(t, ext.MessagingSystemSQS, spans[0].Tag(ext.MessagingSystem))
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
			assert.Equal(t, want, kinesisCase.run(context.Background(), newInjectTestConfig(t)))
		})
	}
}
