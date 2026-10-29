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

// ObserveCluster sets each nil observer of cluster to one contrib Observer
// built from cluster, and reports whether it set any. Observers already set,
// by the application or by the contrib's CreateTracedSession, are kept.
func ObserveCluster(cluster *gocql.ClusterConfig) bool {
	if cluster == nil || (cluster.QueryObserver != nil && cluster.BatchObserver != nil && cluster.ConnectObserver != nil) {
		return false
	}
	obs := gocqltrace.NewObserver(cluster)
	if cluster.QueryObserver == nil {
		cluster.QueryObserver = obs
	}
	if cluster.BatchObserver == nil {
		cluster.BatchObserver = obs
	}
	if cluster.ConnectObserver == nil {
		cluster.ConnectObserver = obs
	}
	return true
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
