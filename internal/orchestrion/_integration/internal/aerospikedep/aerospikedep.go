// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package aerospikedep calls an aerospike client inside a dependency module.
package aerospikedep

import (
	"context"

	as "github.com/aerospike/aerospike-client-go/v7"
)

// Put stores value under key. ctx is the context the instrumentation passes to
// the span.
func Put(ctx context.Context, client *as.Client, key *as.Key, value string) as.Error {
	_ = ctx
	return client.Put(nil, key, as.BinMap{"value": value})
}
