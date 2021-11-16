// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tinylib/msgp/msgp"
)

func TestGroupedStatsEncoding(t *testing.T) {
	gs := groupedStats{
		Service:        "service",
		Name:           "name",
		Resource:       "resource",
		HTTPStatusCode: 1,
		Type:           "type",
		DBType:         "dbtype",
		Hits:           2,
		Errors:         3,
		Duration:       4,
		OkSummary:      []byte("OK SUMMARY"),
		ErrorSummary:   []byte("ERROR SUMMARY"),
		Synthetics:     true,
		TopLevelHits:   5,
	}
	var bs bytes.Buffer
	err := msgp.Encode(&bs, &gs)
	assert.NoError(t, err)

	var gs2 groupedStats
	err = msgp.Decode(&bs, &gs2)
	assert.NoError(t, err)
	assert.Equal(t, gs, gs2)
}

func TestStatsPayloadEncoding(t *testing.T) {
	p := statsPayload{
		Hostname: "hostname",
		Env:      "env",
		Version:  "version",
		Stats: []statsBucket{
			statsBucket{
				Start:    0,
				Duration: 1,
				Stats:    []groupedStats{groupedStats{}},
			},
			statsBucket{
				Start:    2,
				Duration: 3,
				Stats:    []groupedStats{groupedStats{}},
			},
		},
	}

	var bs bytes.Buffer
	err := msgp.Encode(&bs, &p)
	assert.NoError(t, err)

	var p2 statsPayload
	err = msgp.Decode(&bs, &p2)
	assert.NoError(t, err)
	assert.Equal(t, p, p2)
}
