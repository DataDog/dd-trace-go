// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package ext

// Tags specific to AWS.
const (
	AWSServiceLegacy = "aws.service"
	AWSRegionLegacy  = "aws.region"

	AWSAgent      = "aws.agent"
	AWSService    = "aws_service"
	AWSOperation  = "aws.operation"
	AWSRegion     = "region"
	AWSPartition  = "aws.partition"
	AWSRequestID  = "aws.request_id"
	AWSRetryCount = "aws.retry_count"

	SQSQueueName = "queuename"
	// SQSQueueURL is the URL of the SQS queue the request is sent to.
	SQSQueueURL = "aws.sqs.queue.url"

	SNSTargetName = "targetname"
	SNSTopicName  = "topicname"
	// SNSTopicARN is the ARN of the SNS topic the request is sent to.
	SNSTopicARN = "aws.sns.topic_arn"

	DynamoDBTableName = "tablename"

	KinesisStreamName = "streamname"
	// KinesisStreamARN is the ARN of the Kinesis stream the request is sent to.
	KinesisStreamARN = "aws.stream.arn"

	EventBridgeRuleName = "rulename"

	SFNStateMachineName = "statemachinename"
	// SFNStateMachineARN is the ARN of the Step Functions state machine the
	// request is sent to.
	SFNStateMachineARN = "aws.step_functions.state_machine.arn"

	S3BucketName = "bucketname"
)
