// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package hostname

import (
	"context"
	"sync"
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
			result, _, shouldRefresh := getCached(test.now)
			assert.Equal(tt, "oldName", result)
			assert.Equal(tt, test.expected, shouldRefresh)
		})
	}
}

func resetVars() {
	fargatePf = fargate
	SetConfigProvider(nil)
}

func TestGet(t *testing.T) {
	t.Cleanup(resetVars)

	t.Run("FargateEmptyOK", func(t *testing.T) {
		fargatePf = func(_ context.Context) (string, error) {
			return "", nil
		}
		updateHostname(time.Time{})
		result := Get()
		for isRefreshing.Load() == true {
			continue
		} // Wait for extra go routine to finish
		assert.Empty(t, result)
	})

	t.Run("ConfigOK", func(t *testing.T) {
		SetConfigProvider(func() string { return "myConfigHost" })
		updateHostname(time.Time{})
		result := Get()
		for isRefreshing.Load() == true {
			continue
		} // Wait for extra go routine to finish
		assert.Equal(t, "myConfigHost", result)
	})
}

func TestConfigProviderConcurrentSetAndRead(t *testing.T) {
	t.Cleanup(func() { SetConfigProvider(nil) })
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() {
			for range 1_000 {
				if i%2 == 0 {
					SetConfigProvider(func() string { return "host.example" })
				} else {
					_, _ = fromConfig(context.Background(), "")
				}
			}
		})
	}
	wg.Wait()
}
