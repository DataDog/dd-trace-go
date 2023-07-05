// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package tags

const (
	AWSAgent     = "aws.agent"
	AWSService   = "aws_service" // Service aws-sdk request is heading to, ex. S3, SQS
	AWSOperation = "aws.operation"
	AWSRegion    = "region" //AWS Region used to pivot from AWS Integration metrics to traces
	AWSRequestID = "aws.request_id"
)

const (
	SQSQueueName = "queuename"
)

const (
	SNSTargetName = "targetname"
	SNSTopicName  = "topicname"
)

const (
	DynamoDBTableName = "tablename"
)

const (
	KinesisStreamName = "streamname"
)

const (
	EventBridgeRuleName = "rulename"
)

const (
	SFNStateMachineName = "statemachinename"
)

const (
	S3BucketName = "bucketname"
)
