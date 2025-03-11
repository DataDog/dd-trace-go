// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sql

import (
	"sync"
	"testing"

	"github.com/DataDog/datadog-go/v5/statsd"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func applyTags(cfg *config) {
	cfg.serviceName = "my-svc"
	cfg.tags = make(map[string]interface{})
	cfg.tags["tag"] = "value"
}

// Test that statsTags(*config) returns tags from the provided *config + whatever is on the globalconfig
func TestStatsTags(t *testing.T) {
	t.Run("default none", func(t *testing.T) {
		cfg := new(config)
		tags := cfg.statsdExtraTags()
		assert.Len(t, tags, 0)
	})
	t.Run("add tags from config", func(t *testing.T) {
		cfg := new(config)
		applyTags(cfg)
		tags := cfg.statsdExtraTags()
		assert.Len(t, tags, 2)
		assert.Contains(t, tags, "service:my-svc")
		assert.Contains(t, tags, "tag:value")
	})
}

func TestPollDBStatsStop(t *testing.T) {
	driverName := "postgres"
	Register(driverName, &pq.Driver{}, WithService("postgres-test"), WithAnalyticsRate(0.2))
	defer unregister(driverName)
	db, err := Open(driverName, "postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable")
	require.NoError(t, err)
	defer db.Close()

	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		pollDBStats(&statsd.NoOpClientDirect{}, db, stop)
	}()
	close(stop)
	wg.Wait()
}
