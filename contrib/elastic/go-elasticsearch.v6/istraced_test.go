// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package elastic

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsTraced(t *testing.T) {
	assert.True(t, IsTraced(NewRoundTripper()))
	assert.True(t, IsTraced(NewRoundTripper(WithTransport(NewRoundTripper()))))
	assert.False(t, IsTraced(nil))
	assert.False(t, IsTraced(http.DefaultTransport))
}
