// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package awsconfig

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfigsdk "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	awstrace "github.com/DataDog/dd-trace-go/contrib/aws/aws-sdk-go-v2/v2/aws"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
)

func mockAWS(statusCode int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("X-Amz-RequestId", "test_req")
			w.WriteHeader(statusCode)
			w.Write([]byte(`{}`))
		}))
}

func TestWithDataDogTracer(t *testing.T) {
	tests := []struct {
		name               string
		expectedStatusCode int
	}{
		{
			name:               "test mocked sqs failure request",
			expectedStatusCode: 400,
		},
		{
			name:               "test mocked sqs success request",
			expectedStatusCode: 200,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()

			server := mockAWS(tt.expectedStatusCode)
			defer server.Close()

			resolver := aws.EndpointResolverWithOptionsFunc(func(_, _ string, _ ...interface{}) (aws.Endpoint, error) {
				return aws.Endpoint{
					PartitionID:   "aws",
					URL:           server.URL,
					SigningRegion: "eu-west-1",
				}, nil
			})

			awsCfg, err := awsconfigsdk.LoadDefaultConfig(context.Background(),
				awsconfigsdk.WithRegion("eu-west-1"),
				awsconfigsdk.WithCredentialsProvider(aws.AnonymousCredentials{}),
				awsconfigsdk.WithEndpointResolverWithOptions(resolver),
				WithDataDogTracer(),
			)
			require.NoError(t, err)

			sqsClient := sqs.NewFromConfig(awsCfg)
			_, err = sqsClient.SendMessage(context.Background(), &sqs.SendMessageInput{
				MessageBody: aws.String("foobar"),
				QueueUrl:    aws.String("https://sqs.us-west-2.amazonaws.com/123456789012/MyQueueName"),
			})
			if tt.expectedStatusCode >= 400 {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			spans := mt.FinishedSpans()
			require.Len(t, spans, 1)

			s := spans[0]
			assert.Equal(t, "SQS.request", s.OperationName())
			assert.Equal(t, "SendMessage", s.Tag("aws.operation"))
			assert.Equal(t, "SQS", s.Tag("aws.service"))
			assert.Equal(t, "eu-west-1", s.Tag("aws.region"))
			assert.Equal(t, float64(tt.expectedStatusCode), s.Tag(ext.HTTPCode))
		})
	}
}

// TestWithDataDogTracer_ReusedLoadOptionsFunc guards against a bug. A reused WithDataDogTracer
// closure kept a stale *config, because prepConfig ran only once, at construction time. It never
// re-read mutable global state, like the DD_TRACE_AWS_ANALYTICS_ENABLED env var.
//
// This test can't use globalconfig.SetAnalyticsRate or testutils.SetGlobalAnalyticsRate. defaults()
// calls instr.AnalyticsRate(false), which ignores globalconfig.AnalyticsRate() here.
func TestWithDataDogTracer_ReusedLoadOptionsFunc(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	server := mockAWS(200)
	defer server.Close()

	resolver := aws.EndpointResolverWithOptionsFunc(func(_, _ string, _ ...interface{}) (aws.Endpoint, error) {
		return aws.Endpoint{
			PartitionID:   "aws",
			URL:           server.URL,
			SigningRegion: "eu-west-1",
		}, nil
	})

	sendMessage := func(cfg aws.Config) {
		sqsClient := sqs.NewFromConfig(cfg)
		_, err := sqsClient.SendMessage(context.Background(), &sqs.SendMessageInput{
			MessageBody: aws.String("foobar"),
			QueueUrl:    aws.String("https://sqs.us-west-2.amazonaws.com/123456789012/MyQueueName"),
		})
		require.NoError(t, err)
	}

	// Build the LoadOptionsFunc exactly once, as a caller storing it for reuse would.
	loadOpt := WithDataDogTracer()

	// First LoadDefaultConfig call: analytics enabled via the global env var.
	t.Setenv("DD_TRACE_AWS_ANALYTICS_ENABLED", "true")
	cfg1, err := awsconfigsdk.LoadDefaultConfig(context.Background(),
		awsconfigsdk.WithRegion("eu-west-1"),
		awsconfigsdk.WithCredentialsProvider(aws.AnonymousCredentials{}),
		awsconfigsdk.WithEndpointResolverWithOptions(resolver),
		loadOpt,
	)
	require.NoError(t, err)
	sendMessage(cfg1)

	// Flip the global env var, then build a second, independent aws.Config by reusing the SAME
	// stored LoadOptionsFunc (WithDataDogTracer is deliberately not called again).
	t.Setenv("DD_TRACE_AWS_ANALYTICS_ENABLED", "false")
	cfg2, err := awsconfigsdk.LoadDefaultConfig(context.Background(),
		awsconfigsdk.WithRegion("eu-west-1"),
		awsconfigsdk.WithCredentialsProvider(aws.AnonymousCredentials{}),
		awsconfigsdk.WithEndpointResolverWithOptions(resolver),
		loadOpt,
	)
	require.NoError(t, err)
	sendMessage(cfg2)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 2)

	// If prepConfig had run only once (the pre-fix bug), both spans would report the same
	// (first-observed) rate instead of tracking the env var change between calls.
	assert.Equal(t, 1.0, spans[0].Tag(ext.EventSampleRate))
	assert.Nil(t, spans[1].Tag(ext.EventSampleRate))
}

func TestWithDataDogTracer_WithOpts(t *testing.T) {
	tests := []struct {
		name                string
		opts                []awstrace.Option
		expectedServiceName string
		expectedRate        interface{}
	}{
		{
			name:                "with defaults",
			opts:                nil,
			expectedServiceName: "aws.SQS",
			expectedRate:        nil,
		},
		{
			name:                "with enabled",
			opts:                []awstrace.Option{awstrace.WithAnalytics(true)},
			expectedServiceName: "aws.SQS",
			expectedRate:        1.0,
		},
		{
			name:                "with disabled",
			opts:                []awstrace.Option{awstrace.WithAnalytics(false)},
			expectedServiceName: "aws.SQS",
			expectedRate:        nil,
		},
		{
			name:                "with service name",
			opts:                []awstrace.Option{awstrace.WithService("TestName")},
			expectedServiceName: "TestName",
			expectedRate:        nil,
		},
		{
			name:                "with override",
			opts:                []awstrace.Option{awstrace.WithAnalyticsRate(0.23)},
			expectedServiceName: "aws.SQS",
			expectedRate:        0.23,
		},
		{
			name:                "with rate outside boundary",
			opts:                []awstrace.Option{awstrace.WithAnalyticsRate(1.5)},
			expectedServiceName: "aws.SQS",
			expectedRate:        nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()

			server := mockAWS(200)
			defer server.Close()

			resolver := aws.EndpointResolverWithOptionsFunc(func(_, _ string, _ ...interface{}) (aws.Endpoint, error) {
				return aws.Endpoint{
					PartitionID:   "aws",
					URL:           server.URL,
					SigningRegion: "eu-west-1",
				}, nil
			})

			awsCfg, err := awsconfigsdk.LoadDefaultConfig(context.Background(),
				awsconfigsdk.WithRegion("eu-west-1"),
				awsconfigsdk.WithCredentialsProvider(aws.AnonymousCredentials{}),
				awsconfigsdk.WithEndpointResolverWithOptions(resolver),
				WithDataDogTracer(tt.opts...),
			)
			require.NoError(t, err)

			sqsClient := sqs.NewFromConfig(awsCfg)
			sqsClient.ListQueues(context.Background(), &sqs.ListQueuesInput{})

			spans := mt.FinishedSpans()
			assert.Len(t, spans, 1)
			s := spans[0]
			assert.Equal(t, tt.expectedServiceName, s.Tag(ext.ServiceName))
			assert.Equal(t, tt.expectedRate, s.Tag(ext.EventSampleRate))
		})
	}
}
