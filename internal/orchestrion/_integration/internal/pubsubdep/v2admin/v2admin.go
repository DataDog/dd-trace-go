// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package v2admin calls the cloud.google.com/go/pubsub/v2 admin client inside a
// dependency module.
package v2admin

import (
	"context"

	vkit "cloud.google.com/go/pubsub/v2/apiv1"
	"cloud.google.com/go/pubsub/v2/apiv1/pubsubpb"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
)

// ListTopics lists the topics of project with a new TopicAdminClient.
func ListTopics(ctx context.Context, project string, opts ...option.ClientOption) error {
	client, err := vkit.NewTopicAdminClient(ctx, opts...)
	if err != nil {
		return err
	}
	defer client.Close()
	it := client.ListTopics(ctx, &pubsubpb.ListTopicsRequest{Project: "projects/" + project})
	for {
		if _, err := it.Next(); err == iterator.Done {
			return nil
		} else if err != nil {
			return err
		}
	}
}
