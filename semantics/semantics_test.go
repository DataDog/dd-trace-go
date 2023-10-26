package semantics

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLoad(t *testing.T) {
	assert.Len(t, semantics, 2)

	s := Get(HTTP_URL)
	assert.NotNil(t, s)
	assert.Equal(t, HTTP_URL, s.ID)
	assert.Equal(t, "HTTP_URL", s.Name)
	assert.True(t, s.IsSensitive)

	notFound := Get(5)
	assert.Nil(t, notFound)
}
