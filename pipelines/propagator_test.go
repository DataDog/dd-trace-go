// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package pipelines

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestEncode(t *testing.T) {
	now := time.Now().Local().Truncate(time.Millisecond)
	processor := processor{
		service: "service-1",
	}
	setGlobalProcessor(&processor)
	defer setGlobalProcessor(nil)

	p := Pathway{
		hash:         234,
		pathwayStart: now.Add(-time.Hour),
		edgeStart:    now,
	}
	encoded := p.Encode()
	p.service = "service-1"
	decoded, err := Decode(encoded)
	assert.Nil(t, err)
	assert.Equal(t, p, decoded)
}
