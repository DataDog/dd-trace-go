// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package normalizer

import (
	"net/textproto"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"

	"github.com/stretchr/testify/assert"
)

func TestHeaderTagSlice(t *testing.T) {
	t.Run("single", func(t *testing.T) {
		hSlice := []string{"header:tag"}
		hMap := HeaderTagSlice(hSlice)
		assert.Len(t, hMap, 1)
		v, ok := hMap[textproto.CanonicalMIMEHeaderKey("header")]
		assert.True(t, ok)
		assert.Equal(t, "tag", v)
	})
	t.Run("multi", func(t *testing.T) {
		hSlice := []string{"header1:tag1", "header2:tag2"}
		hMap := HeaderTagSlice(hSlice)
		assert.Len(t, hMap, 2)
		v, ok := hMap[textproto.CanonicalMIMEHeaderKey("header1")]
		assert.True(t, ok)
		assert.Equal(t, "tag1", v)
		v, ok = hMap[textproto.CanonicalMIMEHeaderKey("header2")]
		assert.True(t, ok)
		assert.Equal(t, "tag2", v)
	})
	t.Run("datadog headers", func(t *testing.T) {
		hSlice := []string{"x-datadog-id:tag"}
		hMap := HeaderTagSlice(hSlice)
		assert.Len(t, hMap, 1)
	})
	t.Run("leading colon", func(t *testing.T) {
		hSlice := []string{":header"}
		hMap := HeaderTagSlice(hSlice)
		assert.Len(t, hMap, 0)
	})
	t.Run("trailing colon", func(t *testing.T) {
		hSlice := []string{"header:"}
		hMap := HeaderTagSlice(hSlice)
		assert.Len(t, hMap, 0)
	})
}

func TestHeaderTag(t *testing.T) {
	t.Run("single", func(t *testing.T) {
		header, tag := HeaderTag("header")
		assert.Equal(t, "Header", header)
		assert.Equal(t, ext.HTTPRequestHeaders+".header", tag)
	})
	t.Run("mapped", func(t *testing.T) {
		header, tag := HeaderTag("header:tag")
		assert.Equal(t, "Header", header)
		assert.Equal(t, "tag", tag)
	})
	t.Run("whitespaces leading-trailing", func(t *testing.T) {
		header, tag := HeaderTag("  header : tag   ")
		assert.Equal(t, "Header", header)
		assert.Equal(t, "tag", tag)
	})
	t.Run("whitespaces tag", func(t *testing.T) {
		header, tag := HeaderTag("header:t a g")
		assert.Equal(t, "Header", header)
		assert.Equal(t, "t a g", tag)
	})
	t.Run("header special-chars", func(t *testing.T) {
		// when no target tag is specified, the header tag gets normalized
		// on all special chars except '-'
		header, tag := HeaderTag("h-e-a-d-e-r")
		assert.Equal(t, "H-E-A-D-E-R", header)
		assert.Equal(t, ext.HTTPRequestHeaders+".h-e-a-d-e-r", tag)
		header, tag = HeaderTag("h.e.a.d.e.r")
		assert.Equal(t, "H.e.a.d.e.r", header)
		assert.Equal(t, ext.HTTPRequestHeaders+".h_e_a_d_e_r", tag)
		header, tag = HeaderTag("h!e@a*d/e)r")
		assert.Equal(t, "h!e@a*d/e)r", header)
		assert.Equal(t, ext.HTTPRequestHeaders+".h_e_a_d_e_r", tag)
	})
	t.Run("tag special-chars", func(t *testing.T) {
		// no normalization shoul occur on the tag when a target has been specified
		header, tag := HeaderTag("header:t*a.g!")
		assert.Equal(t, "Header", header)
		assert.Equal(t, "t*a.g!", tag)
	})
	t.Run("adjacent-special-chars", func(t *testing.T) {
		_, tag := HeaderTag("h**eader")
		assert.Equal(t, ext.HTTPRequestHeaders+".h__eader", tag)
	})
	t.Run("multi-colon", func(t *testing.T) {
		// split on the last colon; span tag keys cannot contain colons
		header, tag := HeaderTag("header:tag:extra")
		assert.Equal(t, "header:tag", header)
		assert.Equal(t, "extra", tag)
	})
	t.Run("mime-ify header", func(t *testing.T) {
		header, tag := HeaderTag("HEADER")
		assert.Equal(t, "Header", header)
		assert.Equal(t, ext.HTTPRequestHeaders+".header", tag)
	})
	t.Run("lowercase-ify tag", func(t *testing.T) {
		header, tag := HeaderTag("header:TAG")
		assert.Equal(t, "Header", header)
		assert.Equal(t, "tag", tag)
	})
	t.Run("leading colon", func(t *testing.T) {
		header, tag := HeaderTag(":header")
		assert.Equal(t, "", header)
		assert.Equal(t, "header", tag)
	})
	t.Run("trailing colon", func(t *testing.T) {
		header, tag := HeaderTag("header:")
		assert.Equal(t, "Header", header)
		assert.Equal(t, "", tag)
	})
}
