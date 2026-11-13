// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package otelc

import (
	"context"
	"testing"

	"github.com/gocql/gocql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otelc/pkg/hook/hooktest"

	gocqltrace "github.com/DataDog/dd-trace-go/contrib/gocql/gocql/v2"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
)

type appObserver struct{}

func (appObserver) ObserveQuery(context.Context, gocql.ObservedQuery) {}

func TestObserveClusterSetsNilObservers(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	cluster := gocql.NewCluster("10.0.0.1", "10.0.0.2")
	require.True(t, ObserveCluster(cluster))

	obs, ok := cluster.QueryObserver.(*gocqltrace.Observer)
	require.True(t, ok, "QueryObserver is %T", cluster.QueryObserver)
	assert.Same(t, obs, cluster.BatchObserver)
	assert.Same(t, obs, cluster.ConnectObserver)

	// The observer reads the contact points from the cluster it was built from.
	obs.ObserveConnect(gocql.ObservedConnect{})
	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	assert.Equal(t, "10.0.0.1,10.0.0.2", spans[0].Tag("db.cassandra.contact.points"))
}

func TestObserveClusterKeepsObservers(t *testing.T) {
	cluster := gocql.NewCluster("10.0.0.1")
	app := appObserver{}
	cluster.QueryObserver = app
	require.True(t, ObserveCluster(cluster))
	assert.Equal(t, app, cluster.QueryObserver)
	assert.IsType(t, &gocqltrace.Observer{}, cluster.BatchObserver)
	assert.IsType(t, &gocqltrace.Observer{}, cluster.ConnectObserver)

	// Nothing left to set.
	batch := cluster.BatchObserver
	assert.False(t, ObserveCluster(cluster))
	assert.Same(t, batch, cluster.BatchObserver)
	assert.False(t, ObserveCluster(nil))
}

func TestBeforeCreateSessionCopiesCluster(t *testing.T) {
	cluster := &gocql.ClusterConfig{Hosts: []string{"10.0.0.1"}}
	ictx := hooktest.NewMockHookContext(cluster)
	BeforeCreateSession(ictx, cluster)

	traced, ok := ictx.GetParam(0).(*gocql.ClusterConfig)
	require.True(t, ok)
	assert.NotSame(t, cluster, traced)
	assert.IsType(t, &gocqltrace.Observer{}, traced.QueryObserver)
	assert.Nil(t, cluster.QueryObserver, "the application's cluster is unchanged")
	assert.Nil(t, cluster.BatchObserver)
	assert.Nil(t, cluster.ConnectObserver)
}

func TestBeforeCreateSessionLeavesObservedCluster(t *testing.T) {
	cluster := gocql.NewCluster("10.0.0.1")
	ObserveCluster(cluster)
	ictx := hooktest.NewMockHookContext(cluster)
	BeforeCreateSession(ictx, cluster)
	assert.Same(t, cluster, ictx.GetParam(0))

	ictx = hooktest.NewMockHookContext((*gocql.ClusterConfig)(nil))
	BeforeCreateSession(ictx, nil)
	assert.Nil(t, ictx.GetParam(0))
}
