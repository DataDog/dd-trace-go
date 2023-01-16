// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package aws

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/stretchr/testify/assert"
)

func TestAppendMiddleware(t *testing.T) {
	tests := []struct {
		name               string
		responseStatus     int
		responseBody       []byte
		expectedStatusCode int
	}{
		{
			name:               "test mocked sqs failure request",
			responseStatus:     400,
			expectedStatusCode: 400,
		},
		{
			name:               "test mocked sqs success request",
			responseStatus:     200,
			expectedStatusCode: 200,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()

			server := mockAWS(tt.expectedStatusCode)
			defer server.Close()

			resolver := aws.EndpointResolverFunc(func(service, region string) (aws.Endpoint, error) {
				return aws.Endpoint{
					PartitionID:   "aws",
					URL:           server.URL,
					SigningRegion: "eu-west-1",
				}, nil
			})

			awsCfg := aws.Config{
				Region:           "eu-west-1",
				Credentials:      aws.AnonymousCredentials{},
				EndpointResolver: resolver,
			}

			AppendMiddleware(&awsCfg)

			sqsClient := sqs.NewFromConfig(awsCfg)
			sqsClient.ListQueues(context.Background(), &sqs.ListQueuesInput{})

			spans := mt.FinishedSpans()

			s := spans[0]
			assert.Equal(t, "SQS.request", s.OperationName())
			assert.Contains(t, s.Tag(tagAWSAgent), "aws-sdk-go-v2")
			assert.Equal(t, "ListQueues", s.Tag(tagAWSOperation))
			assert.Equal(t, "eu-west-1", s.Tag(tagAWSRegion))
			assert.Equal(t, "SQS.ListQueues", s.Tag(ext.ResourceName))
			assert.Equal(t, "aws.SQS", s.Tag(ext.ServiceName))
			assert.Equal(t, tt.expectedStatusCode, s.Tag(ext.HTTPCode))
			if tt.expectedStatusCode == 200 {
				assert.Equal(t, "test_req", s.Tag("aws.request_id"))
			}
			assert.Equal(t, "POST", s.Tag(ext.HTTPMethod))
			assert.Equal(t, server.URL+"/", s.Tag(ext.HTTPURL))
			assert.Equal(t, "aws/aws-sdk-go-v2/aws", s.Tag(ext.Component))
			assert.Equal(t, ext.SpanKindClient, s.Tag(ext.SpanKind))
		})
	}
}

func TestAppendMiddleware_WithNoTracer(t *testing.T) {
	server := mockAWS(200)
	defer server.Close()

	resolver := aws.EndpointResolverFunc(func(service, region string) (aws.Endpoint, error) {
		return aws.Endpoint{
			PartitionID:   "aws",
			URL:           server.URL,
			SigningRegion: "eu-west-1",
		}, nil
	})

	awsCfg := aws.Config{
		Region:           "eu-west-1",
		Credentials:      aws.AnonymousCredentials{},
		EndpointResolver: resolver,
	}

	AppendMiddleware(&awsCfg)

	sqsClient := sqs.NewFromConfig(awsCfg)
	_, err := sqsClient.ListQueues(context.Background(), &sqs.ListQueuesInput{})
	assert.NoError(t, err)

}

func mockAWS(statusCode int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Amz-RequestId", "test_req")
			w.WriteHeader(statusCode)
			w.Write([]byte(`{}`))
		}))
}

func TestAppendMiddleware_WithOpts(t *testing.T) {
	tests := []struct {
		name                string
		opts                []Option
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
			opts:                []Option{WithAnalytics(true)},
			expectedServiceName: "aws.SQS",
			expectedRate:        1.0,
		},
		{
			name:                "with disabled",
			opts:                []Option{WithAnalytics(false)},
			expectedServiceName: "aws.SQS",
			expectedRate:        nil,
		},
		{
			name:                "with service name",
			opts:                []Option{WithServiceName("TestName")},
			expectedServiceName: "TestName",
			expectedRate:        nil,
		},
		{
			name:                "with override",
			opts:                []Option{WithAnalyticsRate(0.23)},
			expectedServiceName: "aws.SQS",
			expectedRate:        0.23,
		},
		{
			name:                "with rate outside boundary",
			opts:                []Option{WithAnalyticsRate(1.5)},
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

			resolver := aws.EndpointResolverFunc(func(service, region string) (aws.Endpoint, error) {
				return aws.Endpoint{
					PartitionID:   "aws",
					URL:           server.URL,
					SigningRegion: "eu-west-1",
				}, nil
			})

			awsCfg := aws.Config{
				Region:           "eu-west-1",
				Credentials:      aws.AnonymousCredentials{},
				EndpointResolver: resolver,
			}

			AppendMiddleware(&awsCfg, tt.opts...)

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
