package sqltraced

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStringInSlice(t *testing.T) {
	assert := assert.New(t)

	list := []string{"mysql", "postgres", "pq"}
	assert.True(stringInSlice(list, "pq"))
	assert.False(stringInSlice(list, "Postgres"))
}
