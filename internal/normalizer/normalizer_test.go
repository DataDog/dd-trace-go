package normalizer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
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

	t.Run("whitespaces", func(t *testing.T) {
		header, tag := NormalizeHeaderTag("  header : tag   ")
		assert.Equal(t, "header", header)
		assert.Equal(t, "tag", tag)
	})

	t.Run("header-special-chars", func(t *testing.T) {
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

	t.Run("tag-special-chars", func(t *testing.T) {
		// no normalization shoul occur on the tag when a target has been specified
		header, tag := NormalizeHeaderTag("header:t*a.g!")
		assert.Equal(t, "header", header)
		assert.Equal(t, "t*a.g!", tag)
	})

	t.Run("adjacent-special-chars", func(t *testing.T) {
		_, tag := NormalizeHeaderTag("h**eader")
		assert.Equal(t, ext.HTTPRequestHeaders + ".h__eader", tag)
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
		assert.Equal(t, ext.HTTPRequestHeaders + ".header", tag)
	})

	t.Run("lowercase-ify tag", func(t *testing.T) {
		header, tag := NormalizeHeaderTag("header:TAG")
		assert.Equal(t, "header", header)
		assert.Equal(t, "tag", tag)
	})

	// TODO: mtoffl01 - these two colon tests may need to be changed depending on the outcome of this thread:
	// https://github.com/DataDog/dd-trace-go/pull/1764#discussion_r1134574413
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