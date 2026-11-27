// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package main

import "github.com/gocql/gocql"

func init() {
	registerLibrary("gocql", func() {
		_ = gocql.NewCluster("127.0.0.1")
	})
}
