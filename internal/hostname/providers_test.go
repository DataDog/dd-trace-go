package hostname

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestGetCached(t *testing.T) {
	startTime := time.Time{}
	tests := []struct {
		name            string
		cachedAt        time.Time
		cachedAtUpdated bool
		now             time.Time
		expected        bool
	}{
		{
			name:     "CacheExpired",
			cachedAt: startTime,
			now:      startTime.Add(6 * time.Minute),
			expected: true,
		},
		{
			name:     "FreshCache",
			cachedAt: startTime,
			now:      startTime.Add(1 * time.Minute),
			expected: false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(tt *testing.T) {
			cachedHostname = "oldName"
			cachedAt = test.cachedAt
			result, shouldRefresh := getCached(test.now)
			assert.Equal(tt, "oldName", result)
			assert.Equal(tt, test.expected, shouldRefresh)
		})
	}
}
