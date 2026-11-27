// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package main

import (
	"context"

	pubsub "cloud.google.com/go/pubsub/apiv1"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func init() {
	registerLibrary("pubsub_v1", func() {
		// gRPC connects lazily, so nothing dials this endpoint.
		pubsubOpts := []option.ClientOption{
			option.WithEndpoint("localhost:1"),
			option.WithGRPCDialOption(grpc.WithTransportCredentials(insecure.NewCredentials())),
			option.WithoutAuthentication(),
		}
		c, err := pubsub.NewPublisherClient(context.Background(), pubsubOpts...)
		if err != nil {
			panic(err)
		}
		_ = c.Close()
	})
}
