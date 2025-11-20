// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sql

import (
	"context"
	"log"
	"testing"

	"github.com/DataDog/datadog-go/v5/statsd"
	"github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/assert"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"
)

func TestAnalyticsSettings(t *testing.T) {
	t.Run("global", func(t *testing.T) {
		t.Skip("global flag disabled")
		testutils.SetGlobalAnalyticsRate(t, 0.4)

		cfg := new(registerConfig)
		defaults(cfg, "", nil)
		assert.Equal(t, 0.4, cfg.analyticsRate)
	})

	t.Run("enabled", func(t *testing.T) {
		cfg := new(registerConfig)
		defaults(cfg, "", nil)
		WithAnalytics(true)(cfg)
		assert.Equal(t, 1.0, cfg.analyticsRate)
	})

	t.Run("override", func(t *testing.T) {
		testutils.SetGlobalAnalyticsRate(t, 0.4)

		cfg := new(registerConfig)
		defaults(cfg, "", nil)
		WithAnalyticsRate(0.2)(cfg)
		assert.Equal(t, 0.2, cfg.analyticsRate)
	})
}

func TestWithDBStats(t *testing.T) {
	t.Run("default off", func(t *testing.T) {
		cfg := new(config)
		defaults(cfg, "", nil)
		assert.False(t, cfg.dbStats)
	})
	t.Run("on", func(t *testing.T) {
		cfg := new(config)
		defaults(cfg, "", nil)
		WithDBStats()(cfg)
		assert.True(t, cfg.dbStats)
	})
}

func TestCheckStatsdRequired(t *testing.T) {
	t.Run("default none", func(t *testing.T) {
		cfg := new(config)
		cfg.checkStatsdRequired()
		assert.Nil(t, cfg.statsdClient)
	})
	t.Run("dbStats enabled", func(t *testing.T) {
		cfg := new(config)
		cfg.dbStats = true
		cfg.checkStatsdRequired()
		_, ok := cfg.statsdClient.(*statsd.ClientDirect)
		assert.True(t, ok)
	})
	t.Run("invalid address", func(t *testing.T) {
		testutils.SetGlobalDogstatsdAddr(t, "unreachable/socket/path/dsd.socket")
		cfg := new(config)
		cfg.dbStats = true
		cfg.checkStatsdRequired()
		assert.Nil(t, cfg.statsdClient)
		assert.False(t, cfg.dbStats)
	})
}

func TestRegisteredTags(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()
	Register("register-name", &mysql.MySQLDriver{}, WithCustomTag("test", "this-is-a-test"))
	defer unregister("register-name")

	db, err := Open("register-name", "test:test@tcp(127.0.0.1:3306)/test")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	mt.Reset()

	// conduct a normal query to the database. This creates 2 spans
	rows, err := db.QueryContext(context.Background(), "SELECT 1")
	assert.NoError(t, err)
	rows.Close()

	// all created spans should have the tag from Register
	spans := mt.FinishedSpans()
	assert.Len(t, spans, 2)
	for _, sp := range spans {
		assert.Equal(t, "this-is-a-test", sp.Tag("test"))
	}

}
