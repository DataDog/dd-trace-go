// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package immutable_test

import (
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/profiler/internal/immutable"

	"github.com/stretchr/testify/assert"
)

func TestStringSlice(t *testing.T) {
	tags := []string{"service:foo", "env:bar", "ggthingy:baz"}
	f := immutable.NewStringSlice(tags)
	assert.Equal(t, tags, f.Slice())
}

func TestStringSliceModify(t *testing.T) {
	t.Run("modify-original", func(t *testing.T) {
		tags := []string{"service:foo", "env:bar", "thingy:baz"}
		f := immutable.NewStringSlice(tags)
		tags[0] = "service:different"
		assert.Equal(t, "service:foo", f.Slice()[0])
	})

	t.Run("modify-copy", func(t *testing.T) {
		tags := []string{"service:foo", "env:bar", "thingy:baz"}
		f := immutable.NewStringSlice(tags)
		dup := f.Slice()
		dup[0] = "service:different"
		assert.Equal(t, "service:foo", tags[0])
	})

	t.Run("modify-2-copies", func(t *testing.T) {
		tags := []string{"service:foo", "env:bar", "thingy:baz"}
		f := immutable.NewStringSlice(tags)
		dup := f.Slice()
		dup[0] = "service:different"
		dup2 := f.Slice()
		dup2[0] = "service:alsodifferent"
		assert.Equal(t, "service:foo", tags[0])
		assert.Equal(t, "service:different", dup[0])
		assert.Equal(t, "service:alsodifferent", dup2[0])
	})

	t.Run("append-duplicates", func(t *testing.T) {
		var f immutable.StringSlice
		before := f.Slice()
		g := f.Append("foo:bar")
		h := f.Append("other:tag")
		after := g.Slice()
		after2 := h.Slice()
		assert.NotEqual(t, before, after)
		assert.NotEqual(t, before, after2)
		assert.NotEqual(t, after, after2)
	})
}
