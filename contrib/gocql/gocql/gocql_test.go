// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2019 Datadog, Inc.

package gocql

import (
	"context"
	"fmt"
	"log"
	"math"
	"os"
	"testing"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"

	"github.com/gocql/gocql"
	"github.com/stretchr/testify/assert"
)

const (
	debug         = false
	cassandraHost = "127.0.0.1:9042"
)

func newCassandraCluster() *gocql.ClusterConfig {
	cluster := gocql.NewCluster(cassandraHost)
	// the InitialHostLookup must be disabled in newer versions of
	// gocql otherwise "no connections were made when creating the session"
	// error is returned for Cassandra misconfiguration (that we don't need
	// since we're testing another behavior and not the client).
	// Check: https://github.com/gocql/gocql/issues/946
	cluster.DisableInitialHostLookup = true
	// the default timeouts (600ms) are sometimes too short in CI and cause
	// PRs being tested to flake due to this integration.
	cluster.ConnectTimeout = 2 * time.Second
	cluster.Timeout = 2 * time.Second
	return cluster
}

// TestMain sets up the Keyspace and table if they do not exist
func TestMain(m *testing.M) {
	_, ok := os.LookupEnv("INTEGRATION")
	if !ok {
		fmt.Println("--- SKIP: to enable integration test, set the INTEGRATION environment variable")
		os.Exit(0)
	}
	cluster := newCassandraCluster()
	session, err := cluster.CreateSession()
	if err != nil {
		log.Fatalf("%v\n", err)
	}
	// Ensures test keyspace and table person exists.
	session.Query("CREATE KEYSPACE if not exists trace WITH REPLICATION = { 'class' : 'SimpleStrategy', 'replication_factor': 1}").Exec()
	session.Query("CREATE TABLE if not exists trace.person (name text PRIMARY KEY, age int, description text)").Exec()
	session.Query("INSERT INTO trace.person (name, age, description) VALUES ('Cassandra', 100, 'A cruel mistress')").Exec()

	m.Run()
}

func TestErrorWrapper(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	cluster := newCassandraCluster()
	session, err := cluster.CreateSession()
	assert.Nil(err)
	q := session.Query("CREATE KEYSPACE trace WITH REPLICATION = { 'class' : 'NetworkTopologyStrategy', 'datacenter1' : 1 };")
	iter := WrapQuery(q, WithServiceName("ServiceName"), WithResourceName("CREATE KEYSPACE")).Iter()
	err = iter.Close()

	spans := mt.FinishedSpans()
	assert.Len(spans, 1)
	span := spans[0]

	assert.Equal(span.Tag(ext.Error).(error), err)
	assert.Equal(span.OperationName(), ext.CassandraQuery)
	assert.Equal(span.Tag(ext.ResourceName), "CREATE KEYSPACE")
	assert.Equal(span.Tag(ext.ServiceName), "ServiceName")
	assert.Equal(span.Tag(ext.CassandraConsistencyLevel), "QUORUM")
	assert.Equal(span.Tag(ext.CassandraPaginated), "false")

	if iter.Host() != nil {
		assert.Equal(span.Tag(ext.TargetPort), "9042")
		assert.Equal(span.Tag(ext.TargetHost), iter.Host().HostID())
		assert.Equal(span.Tag(ext.CassandraCluster), "datacenter1")
	}
}

func TestChildWrapperSpan(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	// Parent span
	parentSpan, ctx := tracer.StartSpanFromContext(context.Background(), "parentSpan")
	cluster := newCassandraCluster()
	session, err := cluster.CreateSession()
	assert.Nil(err)
	q := session.Query("SELECT * from trace.person")
	tq := WrapQuery(q, WithServiceName("TestServiceName"))
	iter := tq.WithContext(ctx).Iter()
	iter.Close()
	parentSpan.Finish()

	spans := mt.FinishedSpans()
	assert.Len(spans, 2)

	var childSpan, pSpan mocktracer.Span
	if spans[0].ParentID() == spans[1].SpanID() {
		childSpan = spans[0]
		pSpan = spans[1]
	} else {
		childSpan = spans[1]
		pSpan = spans[0]
	}
	assert.Equal(pSpan.OperationName(), "parentSpan")
	assert.Equal(childSpan.ParentID(), pSpan.SpanID())
	assert.Equal(childSpan.OperationName(), ext.CassandraQuery)
	assert.Equal(childSpan.Tag(ext.ResourceName), "SELECT * from trace.person")
	assert.Equal(childSpan.Tag(ext.CassandraKeyspace), "trace")
	if iter.Host() != nil {
		assert.Equal(childSpan.Tag(ext.TargetPort), "9042")
		assert.Equal(childSpan.Tag(ext.TargetHost), iter.Host().HostID())
		assert.Equal(childSpan.Tag(ext.CassandraCluster), "datacenter1")
	}
}

func TestAnalyticsSettings(t *testing.T) {
	assertRate := func(t *testing.T, mt mocktracer.Tracer, rate float64, opts ...WrapOption) {
		cluster := newCassandraCluster()
		session, err := cluster.CreateSession()
		assert.Nil(t, err)
		q := session.Query("CREATE KEYSPACE trace WITH REPLICATION = { 'class' : 'NetworkTopologyStrategy', 'datacenter1' : 1 };")
		iter := WrapQuery(q, opts...).Iter()
		err = iter.Close()

		spans := mt.FinishedSpans()
		assert.Len(t, spans, 1)
		s := spans[0]
		if !math.IsNaN(rate) {
			assert.Equal(t, rate, s.Tag(ext.EventSampleRate))
		}
	}

	t.Run("defaults", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		assertRate(t, mt, globalconfig.AnalyticsRate())
	})

	t.Run("global", func(t *testing.T) {
		t.Skip("global flag disabled")
		mt := mocktracer.Start()
		defer mt.Stop()

		rate := globalconfig.AnalyticsRate()
		defer globalconfig.SetAnalyticsRate(rate)
		globalconfig.SetAnalyticsRate(0.4)

		assertRate(t, mt, 0.4)
	})

	t.Run("enabled", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		assertRate(t, mt, 1.0, WithAnalytics(true))
	})

	t.Run("disabled", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		assertRate(t, mt, math.NaN(), WithAnalytics(false))
	})

	t.Run("override", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		rate := globalconfig.AnalyticsRate()
		defer globalconfig.SetAnalyticsRate(rate)
		globalconfig.SetAnalyticsRate(0.4)

		assertRate(t, mt, 0.23, WithAnalyticsRate(0.23))
	})
}
