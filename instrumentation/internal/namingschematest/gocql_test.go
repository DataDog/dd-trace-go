// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package namingschematest

import (
	"testing"
	"time"

	"github.com/gocql/gocql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gocqltrace "github.com/DataDog/dd-trace-go/contrib/gocql/gocql/v2"
	"github.com/DataDog/dd-trace-go/instrumentation/internal/namingschematest/v2/harness"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

var gocqlTest = harness.TestCase{
	Name: instrumentation.PackageGoCQL,
	GenSpans: func(t *testing.T, serviceOverride string) []*mocktracer.Span {
		var opts []gocqltrace.WrapOption
		if serviceOverride != "" {
			opts = append(opts, gocqltrace.WithService(serviceOverride))
		}
		mt := mocktracer.Start()
		defer mt.Stop()

		nonTraced, err := gocql.NewSession(*gocql.NewCluster("127.0.0.1:9042"))
		require.NoError(t, err)
		// Ensures test keyspace and table person exists.
		require.NoError(t, nonTraced.Query("CREATE KEYSPACE if not exists trace WITH REPLICATION = { 'class' : 'SimpleStrategy', 'replication_factor': 1}").Exec())
		require.NoError(t, nonTraced.Query("CREATE TABLE if not exists trace.person (name text PRIMARY KEY, age int, description text)").Exec())

		cluster := gocqltrace.NewCluster([]string{"127.0.0.1:9042"}, opts...)
		cluster.ConnectTimeout = 2 * time.Second
		cluster.Timeout = 2 * time.Second

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
	},
	WantServiceNameV0: harness.ServiceNameAssertions{
		Defaults:        []string{"gocql.query", "gocql.query"},
		DDService:       []string{"gocql.query", "gocql.query"},
		ServiceOverride: []string{harness.TestServiceOverride, harness.TestServiceOverride},
	},
	AssertOpV0: func(t *testing.T, spans []*mocktracer.Span) {
		require.Len(t, spans, 2)
		assert.Equal(t, "cassandra.query", spans[0].OperationName())
		assert.Equal(t, "cassandra.batch", spans[1].OperationName())
	},
	AssertOpV1: func(t *testing.T, spans []*mocktracer.Span) {
		require.Len(t, spans, 2)
		assert.Equal(t, "cassandra.query", spans[0].OperationName())
		assert.Equal(t, "cassandra.query", spans[1].OperationName())
	},
}
