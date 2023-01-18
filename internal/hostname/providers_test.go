package hostname

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestGetCached(t *testing.T) {
	startTime := time.Time{}
	tests := []struct {
		name     string
		cachedAt time.Time
		now      time.Time
		expected string
	}{
		{
			name:     "CacheExpired",
			cachedAt: startTime,
			now:      startTime.Add(6 * time.Minute),
			expected: "",
		},
		{
			name:     "FreshCache",
			cachedAt: startTime,
			now:      startTime.Add(1 * time.Minute),
			expected: "oldName",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(tt *testing.T) {
			cachedHostname = "oldName"
			cachedAt = test.cachedAt
			result := getCached(test.now)
			assert.Equal(tt, test.expected, result)
		})
	}
}
