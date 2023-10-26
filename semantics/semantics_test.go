package semantics

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLoad(t *testing.T) {
	assert.Len(t, semantics, 2)

	s := Get("HTTP_URL")
	assert.NotNil(t, s)
	assert.Equal(t, 1, s.ID)
	assert.Equal(t, "HTTP_URL", s.Name)
	assert.True(t, s.IsSensitive)

	notFound := Get("SOME_UNKNOWN_THING")
	assert.Nil(t, notFound)
}
