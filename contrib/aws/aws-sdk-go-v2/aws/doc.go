// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package aws provides functions to trace aws/aws-sdk-go-v2 (https://github.com/aws/aws-sdk-go-v2).
//
// # Trace context injection
//
// These environment variables control whether the integration writes the Datadog trace context
// into the requests of each service:
//
//   - DD_TRACE_KINESIS_INJECT_DATADOG_ATTRIBUTE_ENABLED controls PutRecord and PutRecords.
//   - DD_TRACE_EVENTBRIDGE_INJECT_DATADOG_ATTRIBUTE_ENABLED controls PutEvents.
//   - DD_TRACE_SQS_INJECT_DATADOG_ATTRIBUTE_ENABLED controls SendMessage and SendMessageBatch.
//   - DD_TRACE_SNS_INJECT_DATADOG_ATTRIBUTE_ENABLED controls Publish and PublishBatch.
//
// All default to true. Set them before tracer initialization and middleware installation. Existing
// middleware does not reload values after tracer restarts.
//
// False skips new "_datadog" writes, not existing metadata. Spans, the SQS tag and the local Data
// Streams checkpoint remain. Consumers relying on those writes lose fresh trace and pathway context.
//
// Usage Example:
//
//	import (
//		"context"
//		"log"
//		"os"
//
//		"github.com/aws/aws-sdk-go-v2/aws"
//		awscfg "github.com/aws/aws-sdk-go-v2/config"
//		"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
//		"github.com/aws/aws-sdk-go-v2/service/s3"
//		"github.com/aws/aws-sdk-go-v2/service/sqs"
//
//		awstrace "github.com/DataDog/dd-trace-go/contrib/aws/aws-sdk-go-v2/v2/aws"
//		"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
//		"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
//	)
//
//	func Example() {
//		awsCfg, err := awscfg.LoadDefaultConfig(context.Background())
//		if err != nil {
//			log.Fatalf(err.Error())
//		}
//		awstrace.AppendMiddleware(&awsCfg)
//		sqsClient := sqs.NewFromConfig(awsCfg)
//		sqsClient.ListQueues(context.Background(), &sqs.ListQueuesInput{})
//	}
//
//	// An example of the aws span inheriting a parent span from context.
//	func Example_context() {
//		cfg, err := awscfg.LoadDefaultConfig(context.TODO(), awscfg.WithRegion("us-west-2"))
//		if err != nil {
//			log.Fatalf("error: %s", err.Error())
//		}
//		awstrace.AppendMiddleware(&cfg)
//		client := s3.NewFromConfig(cfg)
//		uploader := manager.NewUploader(client)
//
//		// Create a root span.
//		span, ctx := tracer.StartSpanFromContext(context.Background(), "parent.request",
//			tracer.SpanType(ext.SpanTypeWeb),
//			tracer.ResourceName("/upload"),
//		)
//		defer span.Finish()
//
//		// Open image file.
//		filename := "my_image.png"
//		file, err := os.Open(filename)
//		if err != nil {
//			log.Fatalf("error: %s", err.Error())
//		}
//		defer file.Close()
//
//		uploadParams := &s3.PutObjectInput{
//			Bucket:      aws.String("my_bucket"),
//			Key:         aws.String(filename),
//			Body:        file,
//			ContentType: aws.String("image/png"),
//		}
//		// Inherit parent span from context.
//		_, err = uploader.Upload(ctx, uploadParams)
//		if err != nil {
//			log.Fatalf("error: %s", err.Error())
//		}
//	}
package aws
