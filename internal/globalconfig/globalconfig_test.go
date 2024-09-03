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

// Assert that APIs to access cfg.statsTags protect against pollution from external changes
func TestStatsTags(t *testing.T) {
	array := [6]string{"aaa", "bbb", "ccc"}
	slice1 := array[:]
	SetStatsTags(slice1)
	slice1 = append(slice1, []string{"ddd", "eee", "fff"}...)
	slice1[0] = "zzz"
	assert.Equal(t, cfg.statsTags[:3], []string{"aaa", "bbb", "ccc"})

	tags := StatsTags()
	tags[1] = "yyy"
	assert.Equal(t, cfg.statsTags[1], "bbb")
}
