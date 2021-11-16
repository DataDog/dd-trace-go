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
		// These fields indicate the properties under which the stats were aggregated.
		Service:        "service",
		Name:           "name",
		Resource:       "resource",
		HTTPStatusCode: 1,
		Type:           "type",
		DBType:         "dbtype",

		// These fields specify the stats for the above aggregation.
		Hits:         2,
		Errors:       3,
		Duration:     4,
		OkSummary:    []byte("OK SUMMARY"),
		ErrorSummary: []byte("ERROR SUMMARY"),
		Synthetics:   true,
		TopLevelHits: 5,
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
		// Hostname specifies the hostname of the application.
		Hostname: "hostname",

		// Env specifies the env. of the application, as defined by the user.
		Env: "env",

		// Version specifies the application version.
		Version: "version",

		// Stats holds all stats buckets computed within this payload.
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
