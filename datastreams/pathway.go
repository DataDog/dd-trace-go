// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package datastreams

import (
	"context"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/datastreams"
)

type Pathway interface {
	// GetHash returns the hash of the pathway, representing the upstream path of the data.
	GetHash() uint64
}

// PathwayFromContext returns the pathway contained in a Go context if present
func PathwayFromContext(ctx context.Context) (Pathway, bool) {
	return datastreams.PathwayFromContext(ctx)
}
