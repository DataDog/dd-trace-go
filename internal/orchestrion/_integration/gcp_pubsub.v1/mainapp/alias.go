// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package main

// Imported under other names than vkit and option, which the rules must handle
// the way orchestrion does. The client options are spread, so the rule's
// variadic type has to use this file's name for the option package too.
import (
	"context"

	pubsubapi "cloud.google.com/go/pubsub/apiv1"
	"cloud.google.com/go/pubsub/apiv1/pubsubpb"
	"google.golang.org/api/iterator"
	gapioption "google.golang.org/api/option"
	"google.golang.org/api/option/internaloption"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func listSchemas(ctx context.Context, addr string) {
	opts := []gapioption.ClientOption{
		gapioption.WithEndpoint(addr),
		gapioption.WithGRPCDialOption(grpc.WithTransportCredentials(insecure.NewCredentials())),
		gapioption.WithoutAuthentication(),
		internaloption.SkipDialSettingsValidation(),
	}
	client := must(pubsubapi.NewSchemaClient(ctx, opts...))
	it := client.ListSchemas(ctx, &pubsubpb.ListSchemasRequest{Parent: "projects/" + project})
	for {
		if _, err := it.Next(); err == iterator.Done {
			break
		} else {
			check(err)
		}
	}
	check(client.Close())
}
