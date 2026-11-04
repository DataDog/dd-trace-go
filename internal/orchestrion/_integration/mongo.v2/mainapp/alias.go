// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package main

import (
	"context"

	mongooptions "go.mongodb.org/mongo-driver/v2/mongo/options"
)

func insertAliased(ctx context.Context, uri string) {
	insert(ctx, mongooptions.Client().ApplyURI(uri), "alias")
}
