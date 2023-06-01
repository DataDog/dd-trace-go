// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package normalizer

import (
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"

	"github.com/stretchr/testify/assert"
)

func TestNormalizeHeaderTag(t *testing.T) {
	t.Run("single", func(t *testing.T) {
		header, tag := NormalizeHeaderTag("header")
		assert.Equal(t, header, "header")
		assert.Equal(t, ext.HTTPRequestHeaders+".header", tag)
	})
	t.Run("mapped", func(t *testing.T) {
		header, tag := NormalizeHeaderTag("header:tag")
		assert.Equal(t, "header", header)
		assert.Equal(t, "tag", tag)
	})
	t.Run("whitespaces leading-trailing", func(t *testing.T) {
		header, tag := NormalizeHeaderTag("  header : tag   ")
		assert.Equal(t, "header", header)
		assert.Equal(t, "tag", tag)
	})
	t.Run("whitespaces tag", func(t *testing.T) {
		header, tag := NormalizeHeaderTag("header:t a g")
		assert.Equal(t, "header", header)
		assert.Equal(t, "t a g", tag)
	})
	t.Run("header special-chars", func(t *testing.T) {
		// when no target tag is specified, the header tag gets normalized
		// on all special chars except '-'
		header, tag := NormalizeHeaderTag("h-e-a-d-e-r")
		assert.Equal(t, "h-e-a-d-e-r", header)
		assert.Equal(t, ext.HTTPRequestHeaders+".h-e-a-d-e-r", tag)
		header, tag = NormalizeHeaderTag("h.e.a.d.e.r")
		assert.Equal(t, "h.e.a.d.e.r", header)
		assert.Equal(t, ext.HTTPRequestHeaders+".h_e_a_d_e_r", tag)
		header, tag = NormalizeHeaderTag("h!e@a*d/e)r")
		assert.Equal(t, "h!e@a*d/e)r", header)
		assert.Equal(t, ext.HTTPRequestHeaders+".h_e_a_d_e_r", tag)
	})
	t.Run("tag special-chars", func(t *testing.T) {
		// no normalization shoul occur on the tag when a target has been specified
		header, tag := NormalizeHeaderTag("header:t*a.g!")
		assert.Equal(t, "header", header)
		assert.Equal(t, "t*a.g!", tag)
	})
	t.Run("adjacent-special-chars", func(t *testing.T) {
		_, tag := NormalizeHeaderTag("h**eader")
		assert.Equal(t, ext.HTTPRequestHeaders+".h__eader", tag)
	})
	t.Run("multi-colon", func(t *testing.T) {
		// split on the last colon; span tag keys cannot contain colons
		header, tag := NormalizeHeaderTag("header:tag:extra")
		assert.Equal(t, "header:tag", header)
		assert.Equal(t, "extra", tag)
	})
	t.Run("lowercase-ify header", func(t *testing.T) {
		header, tag := NormalizeHeaderTag("HEADER")
		assert.Equal(t, "header", header)
		assert.Equal(t, ext.HTTPRequestHeaders+".header", tag)
	})
	t.Run("lowercase-ify tag", func(t *testing.T) {
		header, tag := NormalizeHeaderTag("header:TAG")
		assert.Equal(t, "header", header)
		assert.Equal(t, "tag", tag)
	})
	t.Run("leading colon", func(t *testing.T) {
		header, tag := NormalizeHeaderTag(":header")
		assert.Equal(t, "", header)
		assert.Equal(t, "header", tag)
	})
	t.Run("trailing colon", func(t *testing.T) {
		header, tag := NormalizeHeaderTag("header:")
		assert.Equal(t, "header", header)
		assert.Equal(t, "", tag)
	})
}
