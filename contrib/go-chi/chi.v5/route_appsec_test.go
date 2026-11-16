// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package chi

import (
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
)

func TestKnownParams(t *testing.T) {
	rctx := chi.NewRouteContext()
	assert.Nil(t, knownParams(rctx))
	rctx.URLParams.Keys, rctx.URLParams.Values = []string{"id", "*", "id"}, []string{"parent", "x", "child"}
	assert.Equal(t, map[string]string{"id": "parent", "*": "x"}, knownParams(rctx))
	rctx.URLParams.Values = rctx.URLParams.Values[:2]
	assert.Nil(t, knownParams(rctx), "the lengths differ")
}
