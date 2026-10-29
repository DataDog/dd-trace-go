// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package gocqldep builds gocql clusters and sessions inside a dependency
// module.
package gocqldep

import (
	"time"

	"github.com/gocql/gocql"
)

// NewCluster returns a cluster config for host.
func NewCluster(host string) *gocql.ClusterConfig {
	return gocql.NewCluster(host)
}

// NewSession returns a session for host, built with gocql.NewSession.
func NewSession(host string) (*gocql.Session, error) {
	return gocql.NewSession(gocql.ClusterConfig{
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
}
