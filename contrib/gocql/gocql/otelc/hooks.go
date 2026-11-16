// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package otelc holds the otelc rules and hooks for github.com/gocql/gocql.
// See otelc.yaml. It is a separate module so otelc never reaches contrib users
// who do not use it.
package otelc

import (
	"github.com/gocql/gocql"
	"go.opentelemetry.io/otelc/pkg/hook"

	gocqltrace "github.com/DataDog/dd-trace-go/contrib/gocql/gocql/v2"
)

// autoObserver is an observer that ObserveCluster set, so that a later call
// can build it again from the cluster's current hosts.
type autoObserver struct{ *gocqltrace.Observer }

// ObserveCluster sets each observer of cluster that is nil, or that an earlier
// call set, to one contrib Observer built from cluster, and reports whether it
// set any. The observer reads the contact points from cluster.Hosts, so hosts
// set after gocql.NewCluster are used once the session is created. Observers
// set by the application or by the contrib's CreateTracedSession are kept.
func ObserveCluster(cluster *gocql.ClusterConfig) bool {
	if cluster == nil {
		return false
	}
	var obs autoObserver
	observer := func() autoObserver {
		if obs.Observer == nil {
			obs = autoObserver{gocqltrace.NewObserver(cluster)}
		}
		return obs
	}
	if replaceable(cluster.QueryObserver) {
		cluster.QueryObserver = observer()
	}
	if replaceable(cluster.BatchObserver) {
		cluster.BatchObserver = observer()
	}
	if replaceable(cluster.ConnectObserver) {
		cluster.ConnectObserver = observer()
	}
	return obs.Observer != nil
}

// replaceable reports whether ObserveCluster may set an observer: it is nil,
// or ObserveCluster set it.
func replaceable(o any) bool {
	if o == nil {
		return true
	}
	_, ok := o.(autoObserver)
	return ok
}

// BeforeCreateSession passes a copy of cluster with the observers set on to
// the original call, so the application's cluster is left unchanged.
func BeforeCreateSession(ictx hook.HookContext, cluster *gocql.ClusterConfig) {
	if cluster == nil {
		return
	}
	traced := *cluster
	if ObserveCluster(&traced) {
		// Parameter 0 is the receiver.
		ictx.SetParam(0, &traced)
	}
}
