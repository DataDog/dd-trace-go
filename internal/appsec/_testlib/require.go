// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package testlib

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// RequireContainsMapSubset requires that the given map m contains the given subset map keys and values.
func RequireContainsMapSubset(t *testing.T, m map[string]interface{}, subset map[string]interface{}) {
	for k, v := range subset {
		require.Contains(t, m, k)
		require.Equal(t, v, m[k])
	}
}
