// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package globalconfig

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestHeaderTag(t *testing.T) {
	SetHeaderTag("header1", "tag1")
	SetHeaderTag("header2", "tag2")

	assert.Equal(t, "tag1", cfg.headersAsTags.Get("header1"))
	assert.Equal(t, "tag2", cfg.headersAsTags.Get("header2"))
}
