package datastreams

import (
	"github.com/stretchr/testify/assert"
	"testing"
	"time"
)

func TestSchemaSampler(t *testing.T) {
	s := &schemaSampler{}
	now := time.Now().UnixNano()
	assert.Equal(t, int64(1), s.sampleSchema(now))
	assert.Equal(t, int64(0), s.sampleSchema(now+10))
	assert.Equal(t, int64(0), s.sampleSchema(now+10))
	assert.Equal(t, int64(3), s.sampleSchema(now+schemaSampleIntervalNs))
}
