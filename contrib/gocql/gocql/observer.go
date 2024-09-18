// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package gocql

import (
	"github.com/gocql/gocql"

	v2 "github.com/DataDog/dd-trace-go/contrib/gocql/gocql/v2"
)

// CreateTracedSession returns a new session augmented with tracing.
func CreateTracedSession(cluster *gocql.ClusterConfig, opts ...WrapOption) (*gocql.Session, error) {
	return v2.CreateTracedSession(cluster, opts...)
}

// NewObserver creates a new Observer to trace gocql.
// This method is useful in case you want to attach the observer to individual traces / batches instead of instrumenting
// the whole client.
func NewObserver(cluster *gocql.ClusterConfig, opts ...WrapOption) *Observer {
	return v2.NewObserver(cluster, opts...)
}

var (
	_ gocql.QueryObserver   = (*Observer)(nil)
	_ gocql.BatchObserver   = (*Observer)(nil)
	_ gocql.ConnectObserver = (*Observer)(nil)
)

// Observer implements gocql observer interfaces to support tracing.
type Observer = v2.Observer
