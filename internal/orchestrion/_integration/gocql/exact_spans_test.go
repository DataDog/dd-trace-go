// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux || !githubci

package gocql

import (
	"context"
	"testing"
	"time"

	"example.com/gocqldep"
	"github.com/DataDog/orchestrion/runtime/built"
	"github.com/gocql/gocql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gocqltrace "github.com/DataDog/dd-trace-go/contrib/gocql/gocql/v2"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/x/agenttest"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/x/tracertest"
	"github.com/DataDog/dd-trace-go/v2/internal/otelc"
)

const (
	componentName = "gocql/gocql"
	querySpan     = "cassandra.query"
	batchSpan     = "cassandra.batch"
	connectSpan   = "cassandra.connect"
)

// TestExactSpans checks the exact gocql spans of each way to build a session
// and of each query, batch and iterator path.
func TestExactSpans(t *testing.T) {
	require.True(t, built.WithOrchestrion || otelc.Enabled(),
		"this test must be run with either orchestrion or otelc enabled")
	var b base
	b.setup(context.Background(), t)
	host := b.hostPort

	// Schema for the query paths, created with an untraced session.
	func() {
		session, err := gocqltrace.NewCluster([]string{host}).CreateSession()
		require.NoError(t, err)
		defer session.Close()
		for _, stmt := range []string{
			"CREATE KEYSPACE IF NOT EXISTS exact WITH REPLICATION = { 'class' : 'SimpleStrategy', 'replication_factor': 1}",
			"CREATE TABLE IF NOT EXISTS exact.person (name text PRIMARY KEY, age int)",
		} {
			require.NoError(t, session.Session.Query(stmt).Exec())
		}
	}()

	t.Run("NewClusterQueryPaths", func(t *testing.T) {
		var connects int
		spans := traced(t, func(ctx context.Context) {
			session, err := gocql.NewCluster(host).CreateSession()
			require.NoError(t, err)
			defer session.Close()

			root, ctx := tracer.StartSpanFromContext(ctx, "test.root")
			defer root.Finish()
			for _, name := range []string{"a", "b", "c"} {
				require.NoError(t, session.Query("INSERT INTO exact.person (name, age) VALUES (?, ?)", name, 1).WithContext(ctx).Exec())
			}
			var age int
			require.NoError(t, session.Query("SELECT age FROM exact.person WHERE name = ?", "a").WithContext(ctx).Scan(&age))

			// A page size of 1 over 3 rows fetches one page per row and a last
			// empty page.
			iter := session.Query("SELECT name FROM exact.person").WithContext(ctx).PageSize(1).Iter()
			var name string
			rows := 0
			for iter.Scan(&name) {
				rows++
			}
			require.NoError(t, iter.Close())
			require.Equal(t, 3, rows)

			batch := session.NewBatch(gocql.LoggedBatch).WithContext(ctx)
			batch.Query("INSERT INTO exact.person (name, age) VALUES (?, ?)", "d", 2)
			batch.Query("INSERT INTO exact.person (name, age) VALUES (?, ?)", "e", 2)
			require.NoError(t, session.ExecuteBatch(batch))

			require.Error(t, session.Query("SELECT nope FROM exact.person").WithContext(ctx).Exec())
		}, &connects)
		root := requireOne(t, spans, "test.root")
		queries := filter(spans, querySpan)
		assert.Len(t, filterResource(queries, "INSERT INTO exact.person (name, age) VALUES (?, ?)"), 3)
		assert.Len(t, filterResource(queries, "SELECT age FROM exact.person WHERE name = ?"), 1)
		assert.Len(t, filterResource(queries, "SELECT name FROM exact.person"), 4)
		failed := filterResource(queries, "SELECT nope FROM exact.person")
		require.Len(t, failed, 1)
		assert.EqualValues(t, 1, failed[0].Error)
		require.Len(t, queries, 9)
		require.Len(t, filter(spans, batchSpan), 1)
		require.Len(t, spans, 11)
		for _, s := range spans {
			if s != root {
				assert.Equal(t, root.SpanID, s.ParentID, "%s is a child of the root", s.Operation)
				assert.Equal(t, host, s.Meta["db.cassandra.contact.points"])
			}
		}
		assert.Positive(t, connects, "connect spans")
		t.Logf("%d connect spans", connects)
	})

	// Each case runs one query under a root and expects queries spans for it.
	sessionCases := []struct {
		name string
		// queries is the number of query spans, per tool when they differ.
		otelcQueries, orchestrionQueries int
		service                          string
		session                          func(t *testing.T) *gocql.Session
	}{
		{
			name:         "StructLiteralPtr",
			otelcQueries: 1, orchestrionQueries: 1,
			session: func(t *testing.T) *gocql.Session {
				cluster := &gocql.ClusterConfig{
					Hosts:                  []string{host},
					CQLVersion:             "3.0.0",
					Timeout:                11 * time.Second,
					ConnectTimeout:         11 * time.Second,
					NumConns:               2,
					Consistency:            gocql.Quorum,
					MaxPreparedStmts:       1000,
					MaxRoutingKeyInfo:      1000,
					PageSize:               5000,
					DefaultTimestamp:       true,
					MaxWaitSchemaAgreement: 60 * time.Second,
					ReconnectInterval:      60 * time.Second,
					ConvictionPolicy:       &gocql.SimpleConvictionPolicy{},
					ReconnectionPolicy:     &gocql.ConstantReconnectionPolicy{MaxRetries: 3, Interval: 1 * time.Second},
					WriteCoalesceWaitTime:  200 * time.Microsecond,
				}
				return createSession(t, cluster)
			},
		},
		{
			name:         "StructLiteralValue",
			otelcQueries: 1, orchestrionQueries: 1,
			session: func(t *testing.T) *gocql.Session {
				cluster := gocql.ClusterConfig{
					Hosts:                  []string{host},
					CQLVersion:             "3.0.0",
					Timeout:                11 * time.Second,
					ConnectTimeout:         11 * time.Second,
					NumConns:               2,
					Consistency:            gocql.Quorum,
					MaxPreparedStmts:       1000,
					MaxRoutingKeyInfo:      1000,
					PageSize:               5000,
					DefaultTimestamp:       true,
					MaxWaitSchemaAgreement: 60 * time.Second,
					ReconnectInterval:      60 * time.Second,
					ConvictionPolicy:       &gocql.SimpleConvictionPolicy{},
					ReconnectionPolicy:     &gocql.ConstantReconnectionPolicy{MaxRetries: 3, Interval: 1 * time.Second},
					WriteCoalesceWaitTime:  200 * time.Microsecond,
				}
				return createSession(t, &cluster)
			},
		},
		{
			// Neither a literal nor NewCluster, which orchestrion does not trace.
			name:         "AssignedFieldsCreateSession",
			otelcQueries: 1, orchestrionQueries: 0,
			session: func(t *testing.T) *gocql.Session {
				return createSession(t, assignedCluster(host))
			},
		},
		{
			name:         "AssignedFieldsNewSession",
			otelcQueries: 1, orchestrionQueries: 0,
			session: func(t *testing.T) *gocql.Session {
				session, err := gocql.NewSession(*assignedCluster(host))
				require.NoError(t, err)
				return session
			},
		},
		{
			name:         "StructLiteralNewSession",
			otelcQueries: 1, orchestrionQueries: 1,
			session: func(t *testing.T) *gocql.Session {
				session, err := gocql.NewSession(gocql.ClusterConfig{
					Hosts:              []string{host},
					CQLVersion:         "3.0.0",
					Timeout:            11 * time.Second,
					ConnectTimeout:     11 * time.Second,
					NumConns:           1,
					Consistency:        gocql.Quorum,
					PageSize:           5000,
					ConvictionPolicy:   &gocql.SimpleConvictionPolicy{},
					ReconnectionPolicy: &gocql.ConstantReconnectionPolicy{MaxRetries: 3, Interval: 1 * time.Second},
				})
				require.NoError(t, err)
				return session
			},
		},
		{
			name:         "ContribCreateTracedSessionNewCluster",
			otelcQueries: 1, orchestrionQueries: 1,
			service: "contrib-service",
			session: func(t *testing.T) *gocql.Session {
				session, err := gocqltrace.CreateTracedSession(gocql.NewCluster(host), gocqltrace.WithService("contrib-service"))
				require.NoError(t, err)
				return session
			},
		},
		{
			name:         "ContribCreateTracedSessionAssigned",
			otelcQueries: 1, orchestrionQueries: 1,
			service: "contrib-service",
			session: func(t *testing.T) *gocql.Session {
				session, err := gocqltrace.CreateTracedSession(assignedCluster(host), gocqltrace.WithService("contrib-service"))
				require.NoError(t, err)
				return session
			},
		},
		{
			// The observers are set on the cluster dependency.NewCluster returns.
			name:         "DependencyNewCluster",
			otelcQueries: 1, orchestrionQueries: 1,
			session: func(t *testing.T) *gocql.Session {
				cluster := gocqldep.NewCluster(host)
				assert.IsType(t, &gocqltrace.Observer{}, cluster.QueryObserver)
				return createSession(t, cluster)
			},
		},
		{
			name:         "DependencyNewSession",
			otelcQueries: 1, orchestrionQueries: 1,
			session: func(t *testing.T) *gocql.Session {
				session, err := gocqldep.NewSession(host)
				require.NoError(t, err)
				return session
			},
		},
	}
	for _, tc := range sessionCases {
		t.Run(tc.name, func(t *testing.T) {
			spans := traced(t, func(ctx context.Context) {
				session := tc.session(t)
				defer session.Close()
				root, ctx := tracer.StartSpanFromContext(ctx, "test.root")
				defer root.Finish()
				require.NoError(t, session.Query("SELECT name FROM exact.person WHERE name = ?", "a").WithContext(ctx).Exec())
			}, nil)
			want := tc.otelcQueries
			if built.WithOrchestrion {
				want = tc.orchestrionQueries
			}
			root := requireOne(t, spans, "test.root")
			queries := filter(spans, querySpan)
			require.Len(t, queries, want)
			require.Len(t, spans, want+1)
			for _, s := range queries {
				assert.Equal(t, root.SpanID, s.ParentID)
				assert.Equal(t, host, s.Meta["db.cassandra.contact.points"])
				if tc.service != "" {
					assert.Equal(t, tc.service, s.Service)
				}
			}
		})
	}

	// A session built with the contrib's deprecated wrappers is traced by the
	// wrappers only.
	t.Run("ContribDeprecatedWrapper", func(t *testing.T) {
		var connects int
		spans := traced(t, func(ctx context.Context) {
			session, err := gocqltrace.NewCluster([]string{host}).CreateSession()
			require.NoError(t, err)
			defer session.Close()
			root, ctx := tracer.StartSpanFromContext(ctx, "test.root")
			defer root.Finish()
			require.NoError(t, session.Query("SELECT name FROM exact.person WHERE name = ?", "a").WithContext(ctx).Exec())
			batch := session.NewBatch(gocql.LoggedBatch).WithContext(ctx)
			batch.Query("INSERT INTO exact.person (name, age) VALUES (?, ?)", "f", 3)
			require.NoError(t, batch.ExecuteBatch(session.Session))
		}, &connects)
		requireOne(t, spans, "test.root")
		require.Len(t, filter(spans, querySpan), 1)
		require.Len(t, filter(spans, batchSpan), 1)
		require.Len(t, spans, 3)
		assert.Zero(t, connects, "the deprecated wrappers report no connect spans")
	})

	// An observer the application set is kept under otelc. Orchestrion replaces
	// the observers of a literal.
	t.Run("ApplicationObserver", func(t *testing.T) {
		obs := &countingObserver{}
		spans := traced(t, func(ctx context.Context) {
			session := createSession(t, &gocql.ClusterConfig{
				Hosts:              []string{host},
				CQLVersion:         "3.0.0",
				Timeout:            11 * time.Second,
				ConnectTimeout:     11 * time.Second,
				NumConns:           1,
				Consistency:        gocql.Quorum,
				PageSize:           5000,
				ConvictionPolicy:   &gocql.SimpleConvictionPolicy{},
				ReconnectionPolicy: &gocql.ConstantReconnectionPolicy{MaxRetries: 3, Interval: 1 * time.Second},
				QueryObserver:      obs,
			})
			defer session.Close()
			root, ctx := tracer.StartSpanFromContext(ctx, "test.root")
			defer root.Finish()
			require.NoError(t, session.Query("SELECT name FROM exact.person WHERE name = ?", "a").WithContext(ctx).Exec())
		}, nil)
		requireOne(t, spans, "test.root")
		if built.WithOrchestrion {
			assert.Zero(t, obs.calls)
			require.Len(t, filter(spans, querySpan), 1)
			return
		}
		assert.Equal(t, 1, obs.calls)
		require.Empty(t, filter(spans, querySpan))
	})
}

// assignedCluster builds a cluster config field by field, without a literal or
// gocql.NewCluster.
func assignedCluster(host string) *gocql.ClusterConfig {
	cluster := new(gocql.ClusterConfig)
	cluster.Hosts = []string{host}
	cluster.CQLVersion = "3.0.0"
	cluster.Timeout = 11 * time.Second
	cluster.ConnectTimeout = 11 * time.Second
	cluster.NumConns = 1
	cluster.Consistency = gocql.Quorum
	cluster.PageSize = 5000
	cluster.ConvictionPolicy = &gocql.SimpleConvictionPolicy{}
	cluster.ReconnectionPolicy = &gocql.ConstantReconnectionPolicy{MaxRetries: 3, Interval: 1 * time.Second}
	return cluster
}

func createSession(t *testing.T, cluster *gocql.ClusterConfig) *gocql.Session {
	t.Helper()
	session, err := cluster.CreateSession()
	require.NoError(t, err)
	return session
}

type countingObserver struct {
	calls int
}

func (o *countingObserver) ObserveQuery(context.Context, gocql.ObservedQuery) {
	o.calls++
}

// traced runs fn with a fresh tracer and returns the test.root span and the
// gocql query and batch spans. Connect spans have no parent and their number
// depends on the driver's pool, so they are only counted into connects.
func traced(t *testing.T, fn func(context.Context), connects *int) []*agenttest.Span {
	t.Helper()
	tr, agent, err := tracertest.Bootstrap(t,
		tracer.WithSampler(tracer.NewAllSampler()),
		tracer.WithLogStartup(false),
	)
	require.NoError(t, err)
	fn(context.Background())
	tr.Stop()
	lister, ok := agent.(agenttest.SpanLister)
	require.True(t, ok, "the test agent must list its spans")
	var spans []*agenttest.Span
	for _, s := range lister.Spans() {
		switch {
		case s.Operation == connectSpan && s.Meta["component"] == componentName:
			if connects != nil {
				*connects++
			}
		case s.Operation == "test.root" || s.Meta["component"] == componentName:
			spans = append(spans, s)
		}
	}
	return spans
}

func filter(spans []*agenttest.Span, operation string) []*agenttest.Span {
	var out []*agenttest.Span
	for _, s := range spans {
		if s.Operation == operation {
			out = append(out, s)
		}
	}
	return out
}

func filterResource(spans []*agenttest.Span, resource string) []*agenttest.Span {
	var out []*agenttest.Span
	for _, s := range spans {
		if s.Resource == resource {
			out = append(out, s)
		}
	}
	return out
}

func requireOne(t *testing.T, spans []*agenttest.Span, operation string) *agenttest.Span {
	t.Helper()
	found := filter(spans, operation)
	require.Len(t, found, 1, "%s spans", operation)
	return found[0]
}
