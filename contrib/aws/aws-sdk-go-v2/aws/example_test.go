// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package aws_test

import (
	"context"
	"log"

	awstrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/aws/aws-sdk-go-v2/aws"

	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
)

func Example() {
	awsCfg, err := awscfg.LoadDefaultConfig(context.Background())
	if err != nil {
		log.Fatalf(err.Error())
	}

	awstrace.AppendMiddleware(&awsCfg)

	sqsClient := sqs.NewFromConfig(awsCfg)
	_, _ = sqsClient.ListQueues(context.Background(), &sqs.ListQueuesInput{})
}

func ExampleLoadOptions() {
	awsCfg, err := awscfg.LoadDefaultConfig(context.TODO(), awstrace.WithDataDogTracer())
	if err != nil {
		log.Fatalf(err.Error())
	}

	sqsClient := sqs.NewFromConfig(awsCfg)
	_, _ = sqsClient.ListQueues(context.Background(), &sqs.ListQueuesInput{})
}
