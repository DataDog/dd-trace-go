package pipelines

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestEncode(t *testing.T) {
	now := time.Now().Local().Truncate(time.Millisecond)
	p := Pathway{
		hash: 234,
		pathwayStart: now.Add(-time.Hour),
		edgeStart: now,
	}
	encoded := p.Encode()
	p.service = "unnamed-go-service"
	decoded, err := Decode(encoded)
	assert.Nil(t, err)
	assert.Equal(t, p, decoded)
}
