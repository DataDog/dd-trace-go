// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package openfeature

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAllocationUnmarshalRequiresBooleanDoLog(t *testing.T) {
	for _, tc := range []struct {
		name string
		json string
		want *bool
	}{
		{name: "true", json: `{"doLog":true}`, want: new(true)},
		{name: "false", json: `{"doLog":false}`, want: new(false)},
		{name: "omitted", json: `{}`},
		{name: "null", json: `{"doLog":null}`},
		{name: "string true", json: `{"doLog":"true"}`},
		{name: "string false", json: `{"doLog":"false"}`},
		{name: "zero", json: `{"doLog":0}`},
		{name: "one", json: `{"doLog":1}`},
		{name: "array", json: `{"doLog":[]}`},
		{name: "object", json: `{"doLog":{}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Reusing an allocation must not hide an omitted field with its old value.
			got := allocation{DoLog: new(true)}
			err := json.Unmarshal([]byte(tc.json), &got)
			if tc.want == nil {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got.DoLog)
		})
	}
}
