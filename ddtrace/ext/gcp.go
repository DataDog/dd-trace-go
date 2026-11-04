// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package ext

// Tags specific to GCP.
const (
	GCPProjectID          = "gcloud.project_id"
	PubsubMessageSize     = "message_size"
	PubsubOrderingKey     = "ordering_key"
	PubsubNumAttributes   = "num_attributes"
	PubsubServerID        = "server_id"
	PubsubMessageID       = "message_id"
	PubsubPublishTime     = "publish_time"
	PubsubDeliveryAttempt = "delivery_attempt"
)
