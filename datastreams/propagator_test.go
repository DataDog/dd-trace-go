// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package datastreams

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func testPathway() Pathway {
	now := time.Now().Local().Truncate(time.Millisecond)
	return Pathway{
		hash:         234,
		pathwayStart: now.Add(-time.Hour),
		edgeStart:    now,
	}
}

func TestEncode(t *testing.T) {
	p := testPathway()
	encoded := p.Encode()
	decoded, err := Decode(encoded)
	assert.Nil(t, err)
	assert.Equal(t, p, decoded)
}

func TestEncodeStr(t *testing.T) {
	p := testPathway()
	encoded := p.EncodeStr()
	decoded, err := DecodeStr(encoded)
	assert.Nil(t, err)
	assert.Equal(t, p, decoded)
}
