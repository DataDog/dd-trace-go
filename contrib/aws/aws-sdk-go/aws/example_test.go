// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package aws_test

import (
	"context"
	"log"
	"os"

	awstrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/aws/aws-sdk-go/aws"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/aws/aws-sdk-go/service/s3/s3manager"
)

// To start tracing requests, wrap the AWS session.Session by invoking
// awstrace.WrapSession.
func Example() {
	cfg := aws.NewConfig().WithRegion("us-west-2")
	sess := session.Must(session.NewSession(cfg))
	sess = awstrace.WrapSession(sess)

	s3api := s3.New(sess)
	s3api.CreateBucket(&s3.CreateBucketInput{
		Bucket: aws.String("some-bucket-name"),
	})
}

// An example of the aws span inheriting a parent span from context.
func Example_context() {
	cfg := aws.NewConfig().WithRegion("us-west-2")
	sess := session.Must(session.NewSession(cfg))
	sess = awstrace.WrapSession(sess)
	uploader := s3manager.NewUploader(sess)

	// Create a root span.
	span, ctx := tracer.StartSpanFromContext(context.Background(), "parent.request",
		tracer.SpanType(ext.SpanTypeWeb),
		tracer.ServiceName("web"),
		tracer.ResourceName("/upload"),
	)
	defer span.Finish()

	// Open image file.
	filename := "my_image.png"
	file, err := os.Open(filename)
	if err != nil {
		log.Fatalf("error: %v", err)
	}
	defer file.Close()

	uploadParams := &s3manager.UploadInput{
		Bucket:      aws.String("my_bucket"),
		Key:         aws.String(filename),
		Body:        file,
		ContentType: aws.String("image/png"),
	}
	// Inherit parent span from context.
	_, err = uploader.UploadWithContext(ctx, uploadParams)
	if err != nil {
		log.Fatalf("error: %v", err)
	}
}
