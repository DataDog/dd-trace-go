// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package gocql

import (
	"testing"
	"time"

	"github.com/gocql/gocql"
	"github.com/stretchr/testify/assert"
	gocqltrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/gocql/gocql"
)

type Integration struct {
	cluster  *gocql.ClusterConfig
	session  *gocql.Session
	numSpans int
	opts     []gocqltrace.WrapOption
}

func New() *Integration {
	return &Integration{
		opts: make([]gocqltrace.WrapOption, 0),
	}
}

func (i *Integration) WithServiceName(name string) {
	i.opts = append(i.opts, gocqltrace.WithServiceName(name))
}

func (i *Integration) Name() string {
	return "gocql/gocql"
}

func (i *Integration) Init(t *testing.T) {
	t.Helper()
	// connect to cluster
	i.cluster = gocql.NewCluster("127.0.0.1:9042")
	i.cluster.DisableInitialHostLookup = true
	// the default timeouts (600ms) are sometimes too short in CI and cause
	// PRs being tested to flake due to this integration.
	i.cluster.ConnectTimeout = 2 * time.Second
	i.cluster.Timeout = 2 * time.Second
	var err error
	i.session, err = i.cluster.CreateSession()
	assert.Nil(t, err)

	i.session.Query("CREATE KEYSPACE if not exists trace WITH REPLICATION = { 'class' : 'SimpleStrategy', 'replication_factor': 1}").Exec()
	i.session.Query("CREATE TABLE if not exists trace.person (name text PRIMARY KEY, age int, description text)").Exec()
	i.session.Query("INSERT INTO trace.person (name, age, description) VALUES ('Cassandra', 100, 'A cruel mistress')").Exec()

	t.Cleanup(func() {
		i.numSpans = 0
	})
}

func (i *Integration) GenSpans(t *testing.T) {
	t.Helper()
	q := i.session.Query("SELECT * from trace.person")
	gocqltrace.WrapQuery(q, i.opts...).Exec()
	q2 := i.session.Query("SELECT * from trace.person")
	gocqltrace.WrapQuery(q2, i.opts...).Exec()
	q3 := i.session.Query("SELECT * from trace.person")
	gocqltrace.WrapQuery(q3, i.opts...).Exec()
	i.numSpans += 3

	b := i.session.NewBatch(gocql.UnloggedBatch)
	tb := gocqltrace.WrapBatch(b, i.opts...)

	stmt := "INSERT INTO trace.person (name, age, description) VALUES (?, ?, ?)"
	tb.Query(stmt, "Kate", 80, "Cassandra's sister running in kubernetes")
	tb.Query(stmt, "Lucas", 60, "Another person")
	err := tb.WithTimestamp(time.Now().Unix() * 1e3).ExecuteBatch(i.session)
	assert.NoError(t, err)
	i.numSpans++
}

func (i *Integration) NumSpans() int {
	return i.numSpans
}
