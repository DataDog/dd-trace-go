// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Command mainapp builds gocql sessions from package main, which the harness
// suites never build. TestSessionsInMain builds and runs it against the
// Cassandra node in GOCQL_TEST_HOST.
package main

import (
	"fmt"
	"os"
	"time"

	"github.com/gocql/gocql"

	gocqltrace "github.com/DataDog/dd-trace-go/contrib/gocql/gocql/v2"
)

func main() {
	host := os.Getenv("GOCQL_TEST_HOST") //nolint:forbidigo // set by TestSessionsInMain

	query(create(observed(gocql.NewCluster(host))), "main_new_cluster")
	query(create(&gocql.ClusterConfig{
		Hosts:              []string{host},
		CQLVersion:         "3.0.0",
		Timeout:            11 * time.Second,
		ConnectTimeout:     11 * time.Second,
		NumConns:           1,
		Consistency:        gocql.Quorum,
		PageSize:           5000,
		ConvictionPolicy:   &gocql.SimpleConvictionPolicy{},
		ReconnectionPolicy: &gocql.ConstantReconnectionPolicy{MaxRetries: 3, Interval: 1 * time.Second},
	}), "main_struct_literal")

	// No rule rewrites the contrib, so this cluster has no observers.
	cluster := *gocqltrace.NewCluster([]string{host}).ClusterConfig
	query(must(gocql.NewSession(cluster)), "main_new_session")

	query(must(gocqltrace.CreateTracedSession(gocql.NewCluster(host))), "main_contrib_traced")

	deprecated := must(gocqltrace.NewCluster([]string{host}).CreateSession())
	if err := deprecated.Query(stmt("main_contrib_deprecated")).Exec(); err != nil {
		panic(err)
	}
	deprecated.Close()

	queryAliased(host)

	// The injected `defer tracer.Stop()` flushes the spans, so main has to
	// return normally.
	fmt.Println("mainapp: done") //nolint:forbidigo
}

func stmt(alias string) string {
	return "SELECT key AS " + alias + " FROM system.local"
}

// observed fails the run when the rules did not set the observers of cluster.
func observed(cluster *gocql.ClusterConfig) *gocql.ClusterConfig {
	if cluster.QueryObserver == nil {
		panic("gocql.NewCluster returned a cluster without observers")
	}
	return cluster
}

func create(cluster *gocql.ClusterConfig) *gocql.Session {
	return must(cluster.CreateSession())
}

func query(session *gocql.Session, alias string) {
	defer session.Close()
	if err := session.Query(stmt(alias)).Exec(); err != nil {
		panic(err)
	}
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
