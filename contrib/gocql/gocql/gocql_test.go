// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package gocql

import (
	"context"
	"fmt"
	"log"
	"math"
	"os"
	"testing"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/namingschematest"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"

	"github.com/gocql/gocql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	cassandraHost = "127.0.0.1:9042"
)

func newCassandraCluster() *gocql.ClusterConfig {
	cfg := gocql.NewCluster(cassandraHost)
	updateTestClusterConfig(cfg)
	return cfg
}

func newTracedCassandraCluster(opts ...WrapOption) *ClusterConfig {
	cfg := NewCluster([]string{cassandraHost}, opts...)
	updateTestClusterConfig(cfg.ClusterConfig)
	return cfg
}

func updateTestClusterConfig(cfg *gocql.ClusterConfig) {
	// the default timeouts (600ms) are sometimes too short in CI and cause
	// PRs being tested to flake due to this integration.
	cfg.ConnectTimeout = 2 * time.Second
	cfg.Timeout = 2 * time.Second
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

	os.Exit(m.Run())
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
	assert.Equal(span.OperationName(), "cassandra.query")
	assert.Equal(span.Tag(ext.ResourceName), "CREATE KEYSPACE")
	assert.Equal(span.Tag(ext.ServiceName), "ServiceName")
	assert.Equal(span.Tag(ext.CassandraConsistencyLevel), "QUORUM")
	assert.Equal(span.Tag(ext.CassandraPaginated), "false")
	assert.Equal(span.Tag(ext.Component), "gocql/gocql")
	assert.Equal(span.Tag(ext.SpanKind), ext.SpanKindClient)
	assert.Equal(span.Tag(ext.DBSystem), "cassandra")
	assert.NotContains(span.Tags(), ext.CassandraContactPoints)

	if iter.Host() != nil {
		assert.Equal(span.Tag(ext.TargetPort), "9042")
		assert.Equal(span.Tag(ext.TargetHost), iter.Host().HostID())
		assert.Equal(span.Tag(ext.CassandraCluster), "dd-trace-go-test-cluster")
		assert.Equal(span.Tag(ext.CassandraDatacenter), "dd-trace-go-test-datacenter")
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

	// Call WithContext before WrapQuery to prove WrapQuery needs to use the query.Context()
	// instead of context.Background()
	q := session.Query("SELECT * FROM trace.person").WithContext(ctx)
	tq := WrapQuery(q, WithServiceName("TestServiceName"))
	iter := tq.Iter()
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
	assert.Equal(childSpan.OperationName(), "cassandra.query")
	assert.Equal(childSpan.Tag(ext.ResourceName), "SELECT * FROM trace.person")
	assert.Equal(childSpan.Tag(ext.CassandraKeyspace), "trace")
	assert.Equal(childSpan.Tag(ext.Component), "gocql/gocql")
	assert.Equal(childSpan.Tag(ext.SpanKind), ext.SpanKindClient)
	assert.Equal(childSpan.Tag(ext.DBSystem), "cassandra")
	assert.NotContains(childSpan.Tags(), ext.CassandraContactPoints)

	if iter.Host() != nil {
		assert.Equal(childSpan.Tag(ext.TargetPort), "9042")
		assert.Equal(childSpan.Tag(ext.TargetHost), iter.Host().HostID())
		assert.Equal(childSpan.Tag(ext.CassandraCluster), "dd-trace-go-test-cluster")
		assert.Equal(childSpan.Tag(ext.CassandraDatacenter), "dd-trace-go-test-datacenter")
	}
}

func TestCompatMode(t *testing.T) {
	genSpans := func(t *testing.T) []mocktracer.Span {
		mt := mocktracer.Start()
		defer mt.Stop()

		cluster := newCassandraCluster()
		session, err := cluster.CreateSession()
		require.NoError(t, err)

		q := session.Query("SELECT * FROM trace.person").WithContext(context.Background())
		tq := WrapQuery(q, WithServiceName("TestServiceName"))
		iter := tq.Iter()
		err = iter.Close()
		require.NoError(t, err)

		spans := mt.FinishedSpans()
		require.Len(t, spans, 1)
		return spans
	}
	testCases := []struct {
		name        string
		gocqlCompat string
		wantCluster string
	}{
		{
			name:        "== v1.65",
			gocqlCompat: "v1.65",
			wantCluster: "dd-trace-go-test-datacenter",
		},
		{
			name:        "< v1.65",
			gocqlCompat: "v1.64",
			wantCluster: "dd-trace-go-test-datacenter",
		},
		{
			name:        "> v1.65",
			gocqlCompat: "v1.66",
			wantCluster: "dd-trace-go-test-cluster",
		},
		{
			name:        "empty",
			gocqlCompat: "",
			wantCluster: "dd-trace-go-test-cluster",
		},
		{
			name:        "bad version",
			gocqlCompat: "bad-version",
			wantCluster: "dd-trace-go-test-cluster",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("DD_TRACE_GOCQL_COMPAT", tc.gocqlCompat)
			spans := genSpans(t)
			s := spans[0]
			assert.Equal(t, s.Tag(ext.TargetPort), "9042")
			assert.NotEmpty(t, s.Tag(ext.TargetHost))
			assert.Equal(t, tc.wantCluster, s.Tag(ext.CassandraCluster))
			assert.Equal(t, "dd-trace-go-test-datacenter", s.Tag(ext.CassandraDatacenter))
		})
	}
}

func TestErrNotFound(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	cluster := newCassandraCluster()
	session, err := cluster.CreateSession()
	assert.Nil(err)

	q := session.Query("SELECT name, age FROM trace.person WHERE name = 'This does not exist'")
	var name string
	var age int

	t.Run("default", func(t *testing.T) {
		tq := WrapQuery(q,
			WithServiceName("TestServiceName"),
			// By default, not using WithErrorCheck, any error is an error from tracing POV
		)
		err = tq.Scan(&name, &age)
		assert.Equal(gocql.ErrNotFound, err, "expected error: there is no data")
		assert.Equal("", name)
		assert.Equal(0, age)

		spans := mt.FinishedSpans()
		assert.Len(spans, 1)

		span := spans[0]
		assert.Equal(span.OperationName(), "cassandra.query")
		assert.Equal(span.Tag(ext.ResourceName), "SELECT name, age FROM trace.person WHERE name = 'This does not exist'")
		assert.NotNil(span.Tag(ext.Error), "trace is marked as an error, default behavior")
	})

	t.Run("WithErrorCheck", func(t *testing.T) {
		tq := WrapQuery(q,
			WithServiceName("TestServiceName"),
			// Typical use of WithErrorCheck -> do not return errors when the error is
			// gocql.ErrNotFound, most of the time this is fine, there is just zero rows
			// of data, but this can be perfectly acceptable. The gocql API returns this
			// as it's a way to figure out when scanning of data should be stopped.
			WithErrorCheck(func(err error) bool { return err != gocql.ErrNotFound }))
		err = tq.Scan(&name, &age)
		assert.Equal(gocql.ErrNotFound, err, "expected error: there is no data")
		assert.Equal("", name)
		assert.Equal(0, age)

		spans := mt.FinishedSpans()
		assert.Len(spans, 2)

		span := spans[1]
		assert.Equal(span.OperationName(), "cassandra.query")
		assert.Equal(span.Tag(ext.ResourceName), "SELECT name, age FROM trace.person WHERE name = 'This does not exist'")
		assert.Nil(span.Tag(ext.Error), "trace is not marked as an error, it just has no data")
	})
}

func TestAnalyticsSettings(t *testing.T) {
	assertRate := func(t *testing.T, mt mocktracer.Tracer, rate float64, opts ...WrapOption) {
		cluster := newCassandraCluster()
		session, err := cluster.CreateSession()
		assert.Nil(t, err)

		// Create a query for testing Iter spans
		q := session.Query("CREATE KEYSPACE trace WITH REPLICATION = { 'class' : 'NetworkTopologyStrategy', 'datacenter1' : 1 };")
		iter := WrapQuery(q, opts...).Iter()
		iter.Close() // this will error, we're inspecting the trace not the error

		// Create a query for testing Scanner spans
		q2 := session.Query("CREATE KEYSPACE trace WITH REPLICATION = { 'class' : 'NetworkTopologyStrategy', 'datacenter1' : 1 };")
		scanner := WrapQuery(q2, opts...).Iter().Scanner()
		scanner.Err() // this will error, we're inspecting the trace not the error

		// Create a batch query for testing Batch spans
		b := WrapBatch(session.NewBatch(gocql.UnloggedBatch), opts...)
		b.Query("CREATE KEYSPACE trace WITH REPLICATION = { 'class' : 'NetworkTopologyStrategy', 'datacenter1' : 1 };")
		b.ExecuteBatch(session) // this will error, we're inspecting the trace not the error

		spans := mt.FinishedSpans()
		assert.Len(t, spans, 3)
		for _, s := range spans {
			if !math.IsNaN(rate) {
				assert.Equal(t, rate, s.Tag(ext.EventSampleRate))
			}
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

func TestIterScanner(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	// Parent span
	parentSpan, ctx := tracer.StartSpanFromContext(context.Background(), "parentSpan")
	cluster := newCassandraCluster()
	session, err := cluster.CreateSession()
	assert.NoError(err)

	q := session.Query("SELECT * from trace.person")
	tq := WrapQuery(q, WithServiceName("TestServiceName"))
	iter := tq.WithContext(ctx).Iter()
	sc := iter.Scanner()
	for sc.Next() {
		var t1, t2, t3 interface{}
		sc.Scan(&t1, t2, t3)
	}
	sc.Err()

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
	assert.Equal(childSpan.OperationName(), "cassandra.query")
	assert.Equal(childSpan.Tag(ext.ResourceName), "SELECT * from trace.person")
	assert.Equal(childSpan.Tag(ext.CassandraKeyspace), "trace")
	assert.Equal(childSpan.Tag(ext.Component), "gocql/gocql")
	assert.Equal(childSpan.Tag(ext.SpanKind), ext.SpanKindClient)
	assert.Equal(childSpan.Tag(ext.DBSystem), "cassandra")
	assert.NotContains(childSpan.Tags(), ext.CassandraContactPoints)
}

func TestBatch(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	// Parent span
	parentSpan, ctx := tracer.StartSpanFromContext(context.Background(), "parentSpan")
	cluster := newCassandraCluster()
	cluster.Keyspace = "trace"
	session, err := cluster.CreateSession()
	assert.NoError(err)

	b := session.NewBatch(gocql.UnloggedBatch)
	tb := WrapBatch(b, WithServiceName("TestServiceName"), WithResourceName("BatchInsert"))

	stmt := "INSERT INTO trace.person (name, age, description) VALUES (?, ?, ?)"
	tb.Query(stmt, "Kate", 80, "Cassandra's sister running in kubernetes")
	tb.Query(stmt, "Lucas", 60, "Another person")
	err = tb.WithContext(ctx).WithTimestamp(time.Now().Unix() * 1e3).ExecuteBatch(session)
	assert.NoError(err)

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
	assert.Equal(childSpan.OperationName(), "cassandra.batch")
	assert.Equal(childSpan.Tag(ext.ResourceName), "BatchInsert")
	assert.Equal(childSpan.Tag(ext.CassandraKeyspace), "trace")
	assert.Equal(childSpan.Tag(ext.Component), "gocql/gocql")
	assert.Equal(childSpan.Tag(ext.SpanKind), ext.SpanKindClient)
	assert.Equal(childSpan.Tag(ext.DBSystem), "cassandra")
	assert.NotContains(childSpan.Tags(), ext.CassandraContactPoints)
}

func TestCassandraContactPoints(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	cluster := NewCluster([]string{cassandraHost, "127.0.0.1:9043"})
	updateTestClusterConfig(cluster.ClusterConfig)

	session, err := cluster.CreateSession()
	require.NoError(t, err)
	q := session.Query("CREATE KEYSPACE IF NOT EXISTS trace WITH REPLICATION = { 'class' : 'NetworkTopologyStrategy', 'datacenter1' : 1 };")
	err = q.Iter().Close()
	require.NoError(t, err)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	span := spans[0]

	assert.Equal(span.OperationName(), "cassandra.query")
	assert.Equal(span.Tag(ext.CassandraContactPoints), "127.0.0.1:9042,127.0.0.1:9043")

	mt.Reset()

	tb := session.NewBatch(gocql.UnloggedBatch)
	stmt := "INSERT INTO trace.person (name, age, description) VALUES (?, ?, ?)"
	tb.Query(stmt, "Kate", 80, "Cassandra's sister running in kubernetes")
	tb.Query(stmt, "Lucas", 60, "Another person")
	err = tb.WithContext(context.Background()).WithTimestamp(time.Now().Unix() * 1e3).ExecuteBatch(session.Session)
	require.NoError(t, err)

	spans = mt.FinishedSpans()
	require.Len(t, spans, 1)
	span = spans[0]

	assert.Equal(span.OperationName(), "cassandra.batch")
	assert.Equal(span.Tag(ext.CassandraContactPoints), "127.0.0.1:9042,127.0.0.1:9043")
}

func TestWithWrapOptions(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	cluster := newTracedCassandraCluster(WithServiceName("test-service"), WithResourceName("cluster-resource"))

	session, err := cluster.CreateSession()
	require.NoError(t, err)
	q := session.Query("CREATE KEYSPACE IF NOT EXISTS trace WITH REPLICATION = { 'class' : 'NetworkTopologyStrategy', 'datacenter1' : 1 };")
	q = q.WithWrapOptions(WithResourceName("test-resource"), WithCustomTag("custom_tag", "value"))
	err = q.Iter().Close()
	require.NoError(t, err)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	span := spans[0]

	assert.Equal(span.OperationName(), "cassandra.query")
	assert.Equal(span.Tag(ext.CassandraContactPoints), "127.0.0.1:9042")
	assert.Equal(span.Tag(ext.ServiceName), "test-service")
	assert.Equal(span.Tag(ext.ResourceName), "test-resource")
	assert.Equal(span.Tag("custom_tag"), "value")

	mt.Reset()

	tb := session.NewBatch(gocql.UnloggedBatch)
	stmt := "INSERT INTO trace.person (name, age, description) VALUES (?, ?, ?)"
	tb.Query(stmt, "Kate", 80, "Cassandra's sister running in kubernetes")
	tb.Query(stmt, "Lucas", 60, "Another person")
	tb = tb.WithContext(context.Background()).WithTimestamp(time.Now().Unix() * 1e3)
	tb = tb.WithWrapOptions(WithResourceName("test-resource"), WithCustomTag("custom_tag", "value"))

	err = tb.ExecuteBatch(session.Session)
	require.NoError(t, err)

	spans = mt.FinishedSpans()
	require.Len(t, spans, 1)
	span = spans[0]

	assert.Equal(span.OperationName(), "cassandra.batch")
	assert.Equal(span.Tag(ext.CassandraContactPoints), "127.0.0.1:9042")
	assert.Equal(span.Tag(ext.ServiceName), "test-service")
	assert.Equal(span.Tag(ext.ResourceName), "test-resource")
	assert.Equal(span.Tag("custom_tag"), "value")
}

func TestWithCustomTag(t *testing.T) {
	cluster := newCassandraCluster()
	cluster.Keyspace = "trace"
	session, err := cluster.CreateSession()
	require.NoError(t, err)

	t.Run("WrapQuery", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		q := session.Query("CREATE KEYSPACE IF NOT EXISTS trace WITH REPLICATION = { 'class' : 'NetworkTopologyStrategy', 'datacenter1' : 1 };")
		iter := WrapQuery(q, WithCustomTag("custom_tag", "value")).Iter()
		err = iter.Close()
		require.NoError(t, err)

		spans := mt.FinishedSpans()
		require.Len(t, spans, 1)

		s0 := spans[0]
		assert.Equal(t, "cassandra.query", s0.OperationName())
		assert.Equal(t, "value", s0.Tag("custom_tag"))
	})
	t.Run("WrapBatch", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		b := session.NewBatch(gocql.UnloggedBatch)
		tb := WrapBatch(b, WithCustomTag("custom_tag", "value"))
		stmt := "INSERT INTO trace.person (name, age, description) VALUES (?, ?, ?)"
		tb.Query(stmt, "Kate", 80, "Cassandra's sister running in kubernetes")
		tb.Query(stmt, "Lucas", 60, "Another person")
		err = tb.WithTimestamp(time.Now().Unix() * 1e3).ExecuteBatch(session)
		require.NoError(t, err)

		spans := mt.FinishedSpans()
		require.Len(t, spans, 1)

		s0 := spans[0]
		assert.Equal(t, "cassandra.batch", s0.OperationName())
		assert.Equal(t, "value", s0.Tag("custom_tag"))
	})
}

func TestNamingSchema(t *testing.T) {
	genSpans := namingschematest.GenSpansFn(func(t *testing.T, serviceOverride string) []mocktracer.Span {
		var opts []WrapOption
		if serviceOverride != "" {
			opts = append(opts, WithServiceName(serviceOverride))
		}
		mt := mocktracer.Start()
		defer mt.Stop()

		cluster := newTracedCassandraCluster(opts...)
		session, err := cluster.CreateSession()
		require.NoError(t, err)

		stmt := "INSERT INTO trace.person (name, age, description) VALUES (?, ?, ?)"

		// generate query span
		err = session.Query(stmt, "name", 30, "description").Exec()
		require.NoError(t, err)

		// generate batch span
		tb := session.NewBatch(gocql.UnloggedBatch)

		tb.Query(stmt, "Kate", 80, "Cassandra's sister running in kubernetes")
		tb.Query(stmt, "Lucas", 60, "Another person")
		err = tb.ExecuteBatch(session.Session)
		require.NoError(t, err)

		return mt.FinishedSpans()
	})
	assertOpV0 := func(t *testing.T, spans []mocktracer.Span) {
		require.Len(t, spans, 2)
		assert.Equal(t, "cassandra.query", spans[0].OperationName())
		assert.Equal(t, "cassandra.batch", spans[1].OperationName())
	}
	assertOpV1 := func(t *testing.T, spans []mocktracer.Span) {
		require.Len(t, spans, 2)
		assert.Equal(t, "cassandra.query", spans[0].OperationName())
		assert.Equal(t, "cassandra.query", spans[1].OperationName())
	}
	wantServiceNameV0 := namingschematest.ServiceNameAssertions{
		WithDefaults:             []string{"gocql.query", "gocql.query"},
		WithDDService:            []string{"gocql.query", "gocql.query"},
		WithDDServiceAndOverride: []string{namingschematest.TestServiceOverride, namingschematest.TestServiceOverride},
	}
	t.Run("ServiceName", namingschematest.NewServiceNameTest(genSpans, wantServiceNameV0))
	t.Run("SpanName", namingschematest.NewSpanNameTest(genSpans, assertOpV0, assertOpV1))
}
