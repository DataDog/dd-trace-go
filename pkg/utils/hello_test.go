package utils

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWelcomeString(t *testing.T) {
	assert := assert.New(t)
	assert.Equal("Hello, from the tracer!\n", Welcome())
}
