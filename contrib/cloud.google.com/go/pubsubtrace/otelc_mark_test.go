// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package pubsubtrace

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPublishTraced(t *testing.T) {
	ctx := context.Background()
	assert.False(t, PublishTraced(ctx))
	assert.False(t, PublishTraced(nil)) //nolint:staticcheck // a nil context must not panic
	assert.True(t, PublishTraced(MarkPublishTraced(ctx)))

	type otherKey struct{}
	assert.True(t, PublishTraced(context.WithValue(MarkPublishTraced(ctx), otherKey{}, 1)),
		"the mark must survive contexts derived from the marked one")
}
