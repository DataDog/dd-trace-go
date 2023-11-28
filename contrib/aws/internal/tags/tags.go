// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package tags

const (
	// OldAWSService is a duplicate tag that will be phased out in favor of AWSService.
	OldAWSService = "aws.service"
	// OldAWSRegion is a duplicate tag that will be phased out in favor of AWSRegion.
	OldAWSRegion = "aws.region"

	AWSAgent      = "aws.agent"
	AWSService    = "aws_service"
	AWSOperation  = "aws.operation"
	AWSRegion     = "region"
	AWSRequestID  = "aws.request_id"
	AWSRetryCount = "aws.retry_count"

	SQSQueueName = "queuename"

	SNSTargetName = "targetname"
	SNSTopicName  = "topicname"

	DynamoDBTableName = "tablename"

	KinesisStreamName = "streamname"

	EventBridgeRuleName = "rulename"

	SFNStateMachineName = "statemachinename"

	S3BucketName = "bucketname"
)
