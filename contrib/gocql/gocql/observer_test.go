// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package gocql

import (
	"context"
	"testing"

	"github.com/gocql/gocql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

func TestObserver_Query(t *testing.T) {
	testCases := []struct {
		name             string
		opts             []WrapOption
		updateQuery      func(cluster *gocql.ClusterConfig, sess *gocql.Session, q *gocql.Query) *gocql.Query
		wantServiceName  string
		wantResourceName string
		wantRowCount     int
		wantErr          bool
		wantErrTag       bool
	}{
		{
			name:         "default",
			opts:         nil,
			wantRowCount: 1,
		},
		{
			name: "service_and_resource_name",
			opts: []WrapOption{
				WithServiceName("test-service"),
				WithResourceName("test-resource"),
			},
			wantRowCount:     1,
			wantServiceName:  "test-service",
			wantResourceName: "test-resource",
		},
		{
			name: "error",
			opts: nil,
			updateQuery: func(_ *gocql.ClusterConfig, sess *gocql.Session, _ *gocql.Query) *gocql.Query {
				stmt := "SELECT name, age FRM trace.person WHERE name = 'This does not exist'"
				return sess.Query(stmt)
			},
			wantServiceName:  "",
			wantResourceName: "SELECT name, age FRM trace.person WHERE name = 'This does not exist'",
			wantRowCount:     0,
			wantErr:          true,
			wantErrTag:       true,
		},
		{
			name: "error_ignore",
			opts: []WrapOption{
				WithErrorCheck(func(_ error) bool {
					return false
				}),
			},
			updateQuery: func(_ *gocql.ClusterConfig, sess *gocql.Session, _ *gocql.Query) *gocql.Query {
				stmt := "SELECT name, age FRM trace.person WHERE name = 'This does not exist'"
				return sess.Query(stmt)
			},
			wantServiceName:  "",
			wantResourceName: "SELECT name, age FRM trace.person WHERE name = 'This does not exist'",
			wantRowCount:     0,
			wantErr:          true,
			wantErrTag:       false,
		},
		{
			name: "individual_query_trace",
			opts: []WrapOption{
				WithTraceQuery(false),
			},
			updateQuery: func(cluster *gocql.ClusterConfig, _ *gocql.Session, q *gocql.Query) *gocql.Query {
				obs := NewObserver(cluster, WithResourceName("test resource"), WithServiceName("test service"))
				return q.Observer(obs)
			},
			wantServiceName:  "test service",
			wantResourceName: "test resource",
			wantRowCount:     1,
			wantErr:          false,
			wantErrTag:       false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()

			cluster := newCassandraCluster()
			cluster.Hosts = []string{cassandraHost, "127.0.0.1:9043"}
			cluster.Keyspace = "trace"

			opts := []WrapOption{
				WithTraceQuery(true),
				WithTraceBatch(false),
				WithTraceConnect(false),
			}
			opts = append(opts, tc.opts...)
			sess, err := CreateTracedSession(cluster, opts...)
			require.NoError(t, err)

			p, ctx := tracer.StartSpanFromContext(context.Background(), "parentSpan")

			stmt := "SELECT * FROM trace.person WHERE name = 'Cassandra'"
			q := sess.Query(stmt)
			if tc.updateQuery != nil {
				q = tc.updateQuery(cluster, sess, q)
			}
			q = q.WithContext(ctx)

			err = q.Exec()
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			p.Finish()

			spans := mt.FinishedSpans()
			require.Len(t, spans, 2)

			wantService := tc.wantServiceName
			if wantService == "" {
				wantService = "gocql.query"
			}
			wantResource := tc.wantResourceName
			if wantResource == "" {
				wantResource = stmt
			}
			wantRowCount := tc.wantRowCount

			parentSpan := spans[1]
			querySpan := spans[0]

			assert.Equal(t, "parentSpan", parentSpan.OperationName())
			assert.Equal(t, querySpan.ParentID(), parentSpan.SpanID())

			assertCommonTags(t, querySpan)

			assert.Equal(t, "cassandra.query", querySpan.OperationName())
			assert.Equal(t, wantResource, querySpan.Tag(ext.ResourceName))
			assert.Equal(t, wantService, querySpan.Tag(ext.ServiceName))
			assert.Equal(t, wantRowCount, querySpan.Tag(ext.CassandraRowCount))

			if tc.wantErrTag {
				assert.NotNil(t, querySpan.Tag(ext.Error))
			} else {
				assert.Nil(t, querySpan.Tag(ext.Error))
			}
		})
	}
}

func TestObserver_Batch(t *testing.T) {
	testCases := []struct {
		name             string
		opts             []WrapOption
		updateBatch      func(cluster *gocql.ClusterConfig, sess *gocql.Session, b *gocql.Batch) *gocql.Batch
		wantServiceName  string
		wantResourceName string
		wantErr          bool
		wantErrTag       bool
	}{
		{
			name: "default",
			opts: nil,
		},
		{
			name: "service_and_resource_name",
			opts: []WrapOption{
				WithServiceName("test-service"),
				WithResourceName("test-resource"),
			},
			wantServiceName:  "test-service",
			wantResourceName: "test-resource",
		},
		{
			name: "error",
			opts: nil,
			updateBatch: func(_ *gocql.ClusterConfig, sess *gocql.Session, _ *gocql.Batch) *gocql.Batch {
				stmt := "SELECT name, age FRM trace.person WHERE name = 'This does not exist'"
				b := sess.NewBatch(gocql.UnloggedBatch)
				b.Query(stmt)
				return b
			},
			wantServiceName:  "",
			wantResourceName: "",
			wantErr:          true,
			wantErrTag:       true,
		},
		{
			name: "error_ignore",
			opts: []WrapOption{
				WithErrorCheck(func(_ error) bool {
					return false
				}),
			},
			updateBatch: func(_ *gocql.ClusterConfig, sess *gocql.Session, _ *gocql.Batch) *gocql.Batch {
				stmt := "SELECT name, age FRM trace.person WHERE name = 'This does not exist'"
				b := sess.NewBatch(gocql.UnloggedBatch)
				b.Query(stmt)
				return b
			},
			wantServiceName:  "",
			wantResourceName: "",
			wantErr:          true,
			wantErrTag:       false,
		},
		{
			name: "individual_batch_trace",
			opts: []WrapOption{
				WithTraceBatch(false),
			},
			updateBatch: func(cluster *gocql.ClusterConfig, _ *gocql.Session, b *gocql.Batch) *gocql.Batch {
				obs := NewObserver(cluster, WithResourceName("test resource"), WithServiceName("test service"))
				return b.Observer(obs)
			},
			wantServiceName:  "test service",
			wantResourceName: "test resource",
			wantErr:          false,
			wantErrTag:       false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()

			cluster := newCassandraCluster()
			cluster.Hosts = []string{cassandraHost, "127.0.0.1:9043"}
			cluster.Keyspace = "trace"

			opts := []WrapOption{
				WithTraceQuery(true),
				WithTraceBatch(true),
				WithTraceConnect(false),
			}
			opts = append(opts, tc.opts...)
			sess, err := CreateTracedSession(cluster, opts...)
			require.NoError(t, err)

			p, ctx := tracer.StartSpanFromContext(context.Background(), "parentSpan")

			stmt := "INSERT INTO trace.person (name, age, description) VALUES (?, ?, ?)"
			b := sess.NewBatch(gocql.UnloggedBatch)
			b.Query(stmt, "Kate", 80, "Cassandra's sister running in kubernetes")
			b.Query(stmt, "Lucas", 60, "Another person")

			if tc.updateBatch != nil {
				b = tc.updateBatch(cluster, sess, b)
			}
			b = b.WithContext(ctx)

			err = sess.ExecuteBatch(b)
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			p.Finish()

			spans := mt.FinishedSpans()
			require.Len(t, spans, 2)

			wantService := tc.wantServiceName
			if wantService == "" {
				wantService = "gocql.query"
			}
			wantResource := tc.wantResourceName
			if wantResource == "" {
				wantResource = "cassandra.batch"
			}

			parentSpan := spans[1]
			batchSpan := spans[0]

			assert.Equal(t, "parentSpan", parentSpan.OperationName())
			assert.Equal(t, batchSpan.ParentID(), parentSpan.SpanID())

			assertCommonTags(t, batchSpan)

			assert.Equal(t, "cassandra.batch", batchSpan.OperationName())
			assert.Equal(t, wantResource, batchSpan.Tag(ext.ResourceName))
			assert.Equal(t, wantService, batchSpan.Tag(ext.ServiceName))
			assert.Nil(t, batchSpan.Tag(ext.CassandraRowCount))

			if tc.wantErrTag {
				assert.NotNil(t, batchSpan.Tag(ext.Error))
			} else {
				assert.Nil(t, batchSpan.Tag(ext.Error))
			}
		})
	}
}

func TestObserver_Connect(t *testing.T) {
	testCases := []struct {
		name             string
		opts             []WrapOption
		wantServiceName  string
		wantResourceName string
		wantErr          bool
		wantErrTag       bool
	}{
		{
			name: "default",
			opts: nil,
		},
		{
			name: "service_and_resource_name",
			opts: []WrapOption{
				WithServiceName("test-service"),
				WithResourceName("test-resource"),
			},
			wantServiceName:  "test-service",
			wantResourceName: "test-resource",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()

			cluster := newCassandraCluster()
			cluster.Hosts = []string{cassandraHost, "127.0.0.1:9043"}
			cluster.Keyspace = "trace"

			opts := []WrapOption{
				WithTraceQuery(false),
				WithTraceBatch(false),
				WithTraceConnect(true),
			}
			opts = append(opts, tc.opts...)
			sess, err := CreateTracedSession(cluster, opts...)
			require.NoError(t, err)

			err = sess.Query("SELECT * FROM trace.person WHERE name = 'Cassandra'").Exec()
			require.NoError(t, err)

			wantService := tc.wantServiceName
			if wantService == "" {
				wantService = "gocql.query"
			}
			wantResource := tc.wantResourceName
			if wantResource == "" {
				wantResource = "cassandra.connect"
			}

			spans := mt.FinishedSpans()

			var okSpans []mocktracer.Span
			var okSpansHostInfo []mocktracer.Span
			var errSpans []mocktracer.Span

			for _, span := range spans {
				port := span.Tag(ext.TargetPort)
				require.NotEmpty(t, port)
				switch port {
				case "9042":
					okSpans = append(okSpans, span)

				case "9043":
					errSpans = append(errSpans, span)

				default:
					assert.FailNow(t, "unexpected port: "+port.(string))
				}
			}
			assert.NotEmpty(t, okSpans)
			// the errSpans slice might be empty or not, so we don't assert any length to avoid flakiness.

			for _, span := range spans {
				// this information should be present in all spans.
				assert.Equal(t, "cassandra.connect", span.OperationName())
				assert.Equal(t, wantResource, span.Tag(ext.ResourceName))
				assert.Equal(t, wantService, span.Tag(ext.ServiceName))

				assert.Equal(t, "gocql/gocql", span.Tag(ext.Component))
				assert.Equal(t, ext.SpanKindClient, span.Tag(ext.SpanKind))
				assert.Equal(t, "cassandra", span.Tag(ext.DBSystem))
				assert.Equal(t, "127.0.0.1:9042,127.0.0.1:9043", span.Tag(ext.CassandraContactPoints))
				assert.Equal(t, "127.0.0.1", span.Tag(ext.TargetHost))
			}
			for _, span := range okSpans {
				assert.Equal(t, "9042", span.Tag(ext.TargetPort))
				assert.Nil(t, span.Tag(ext.Error))

				if span.Tag(ext.CassandraHostID) != nil {
					okSpansHostInfo = append(okSpansHostInfo, span)
				}
			}
			assert.NotEmpty(t, okSpansHostInfo, "should have found at least one non-error connect span with additional host info")

			for _, span := range okSpansHostInfo {
				// this information is not present in all the spans for some reason.
				assert.Equal(t, "dd-trace-go-test-cluster", span.Tag(ext.CassandraCluster))
				assert.Equal(t, "dd-trace-go-test-datacenter", span.Tag(ext.CassandraDatacenter))
				assert.NotEmpty(t, span.Tag(ext.CassandraHostID))
			}
			for _, span := range errSpans {
				assert.Equal(t, "9043", span.Tag(ext.TargetPort))
				assert.NotNil(t, span.Tag(ext.Error))

				// since this node does not exist, this information should not be present.
				assert.Nil(t, span.Tag(ext.CassandraCluster))
				assert.Nil(t, span.Tag(ext.CassandraDatacenter))
				assert.Nil(t, span.Tag(ext.CassandraHostID))
			}
		})
	}
}

func assertCommonTags(t *testing.T, span mocktracer.Span) {
	t.Helper()

	assert.Equal(t, "trace", span.Tag(ext.CassandraKeyspace))
	assert.Equal(t, "gocql/gocql", span.Tag(ext.Component))
	assert.Equal(t, ext.SpanKindClient, span.Tag(ext.SpanKind))
	assert.Equal(t, "cassandra", span.Tag(ext.DBSystem))
	assert.Equal(t, "127.0.0.1:9042,127.0.0.1:9043", span.Tag(ext.CassandraContactPoints))
	assert.Equal(t, "9042", span.Tag(ext.TargetPort))
	assert.Equal(t, "127.0.0.1", span.Tag(ext.TargetHost))
	assert.Equal(t, "dd-trace-go-test-cluster", span.Tag(ext.CassandraCluster))
	assert.Equal(t, "dd-trace-go-test-datacenter", span.Tag(ext.CassandraDatacenter))
	assert.NotEmpty(t, span.Tag(ext.CassandraHostID))

	// These tags can't be obtained with the Observer API.
	assert.Nil(t, span.Tag(ext.CassandraPaginated))
	assert.Nil(t, span.Tag(ext.CassandraConsistencyLevel))
}
