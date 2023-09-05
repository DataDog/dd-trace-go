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
