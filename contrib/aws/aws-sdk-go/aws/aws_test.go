// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2020 Datadog, Inc.

package aws

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/stretchr/testify/assert"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
)

func TestAWS(t *testing.T) {
	cfg := aws.NewConfig().
		WithRegion("us-west-2").
		WithDisableSSL(true).
		WithCredentials(credentials.AnonymousCredentials)

	session := WrapSession(session.Must(session.NewSession(cfg)))

	t.Run("s3", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		root, ctx := tracer.StartSpanFromContext(context.Background(), "test")
		s3api := s3.New(session)
		s3api.CreateBucketWithContext(ctx, &s3.CreateBucketInput{
			Bucket: aws.String("BUCKET"),
		})
		root.Finish()

		spans := mt.FinishedSpans()
		assert.Len(t, spans, 2)
		assert.Equal(t, spans[1].TraceID(), spans[0].TraceID())

		s := spans[0]
		assert.Equal(t, "s3.command", s.OperationName())
		assert.Contains(t, s.Tag(tagAWSAgent), "aws-sdk-go")
		assert.Equal(t, "CreateBucket", s.Tag(tagAWSOperation))
		assert.Equal(t, "us-west-2", s.Tag(tagAWSRegion))
		assert.Equal(t, "s3.CreateBucket", s.Tag(ext.ResourceName))
		assert.Equal(t, "aws.s3", s.Tag(ext.ServiceName))
		assert.Equal(t, "403", s.Tag(ext.HTTPCode))
		assert.Equal(t, "PUT", s.Tag(ext.HTTPMethod))
		assert.Equal(t, "http://s3.us-west-2.amazonaws.com/BUCKET", s.Tag(ext.HTTPURL))
	})

	t.Run("ec2", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		root, ctx := tracer.StartSpanFromContext(context.Background(), "test")
		ec2api := ec2.New(session)
		ec2api.DescribeInstancesWithContext(ctx, &ec2.DescribeInstancesInput{})
		root.Finish()

		spans := mt.FinishedSpans()
		assert.Len(t, spans, 2)
		assert.Equal(t, spans[1].TraceID(), spans[0].TraceID())

		s := spans[0]
		assert.Equal(t, "ec2.command", s.OperationName())
		assert.Contains(t, s.Tag(tagAWSAgent), "aws-sdk-go")
		assert.Equal(t, "DescribeInstances", s.Tag(tagAWSOperation))
		assert.Equal(t, "us-west-2", s.Tag(tagAWSRegion))
		assert.Equal(t, "ec2.DescribeInstances", s.Tag(ext.ResourceName))
		assert.Equal(t, "aws.ec2", s.Tag(ext.ServiceName))
		assert.Equal(t, "400", s.Tag(ext.HTTPCode))
		assert.Equal(t, "POST", s.Tag(ext.HTTPMethod))
		assert.Equal(t, "http://ec2.us-west-2.amazonaws.com/", s.Tag(ext.HTTPURL))
	})
}

func TestAnalyticsSettings(t *testing.T) {
	cfg := aws.NewConfig().
		WithRegion("us-west-2").
		WithDisableSSL(true).
		WithCredentials(credentials.AnonymousCredentials)

	session := session.Must(session.NewSession(cfg))
	assertRate := func(t *testing.T, mt mocktracer.Tracer, rate interface{}, opts ...Option) {
		ws := WrapSession(session, opts...)
		ec2.New(ws).DescribeInstancesWithContext(context.TODO(), &ec2.DescribeInstancesInput{})
		spans := mt.FinishedSpans()
		assert.Len(t, spans, 1)
		s := spans[0]
		assert.Equal(t, rate, s.Tag(ext.EventSampleRate))
	}

	t.Run("defaults", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		assertRate(t, mt, nil)
	})

	t.Run("global", func(t *testing.T) {
		t.Skip("global flag disabled")
		mt := mocktracer.Start()
		defer mt.Stop()

		rate := globalconfig.AnalyticsRate()
		defer globalconfig.SetAnalyticsRate(rate)
		globalconfig.SetAnalyticsRate(0.4)

		assertRate(t, mt, 0.4)
	})

	t.Run("enabled", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		assertRate(t, mt, 1.0, WithAnalytics(true))
	})

	t.Run("disabled", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		assertRate(t, mt, nil, WithAnalytics(false))
	})

	t.Run("override", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		rate := globalconfig.AnalyticsRate()
		defer globalconfig.SetAnalyticsRate(rate)
		globalconfig.SetAnalyticsRate(0.4)

		assertRate(t, mt, 0.23, WithAnalyticsRate(0.23))
	})
}

func TestRetries(t *testing.T) {
	cfg := aws.NewConfig().
		WithRegion("us-west-2").
		WithDisableSSL(true).
		WithCredentials(credentials.AnonymousCredentials)

	session := WrapSession(session.Must(session.NewSession(cfg)))
	expectedError := errors.New("an error")
	session.Handlers.Send.PushBack(func(r *request.Request) {
		r.Error = expectedError
		r.Retryable = aws.Bool(true)
	})

	mt := mocktracer.Start()
	defer mt.Stop()

	ctx := context.Background()
	s3api := s3.New(session)
	req, _ := s3api.GetObjectRequest(&s3.GetObjectInput{
		Bucket: aws.String("BUCKET"),
		Key:    aws.String("KEY"),
	})
	req.SetContext(ctx)
	err := req.Send()

	assert.Equal(t, 3, req.RetryCount)
	assert.Same(t, expectedError, err)
	assert.Len(t, mt.OpenSpans(), 0)
	assert.Len(t, mt.FinishedSpans(), 1)
	assert.Equal(t, mt.FinishedSpans()[0].Tag(tagAWSRetryCount), 3)
}
