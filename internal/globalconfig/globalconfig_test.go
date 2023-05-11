// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package globalconfig

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestHeaderTags(t *testing.T) {
	ClearHeaderTags()
	SetHeaderTag("header1", "tag1")
	SetHeaderTag("header2", "tag2")

	assert.Equal(t, "tag1", cfg.headersAsTags["header1"])
	assert.Equal(t, "tag2", cfg.headersAsTags["header2"])

	// This chunk essentially confirms that the globalconfig header tags is passed by value
	// not by reference.
	cp := GetAllHeaderTags()
	assert.Equal(t, "tag1", cp["header1"])
	assert.Equal(t, "tag2", cp["header2"])

	delete(cp, "header1")
	delete(cp, "header2")
	assert.Len(t, cp, 0)

	// Ensure the globalconfig remains untouched
	assert.Equal(t, "tag1", cfg.headersAsTags["header1"])
	assert.Equal(t, "tag2", cfg.headersAsTags["header2"])
}
