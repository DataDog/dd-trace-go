// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package main

import "github.com/graph-gophers/graphql-go"

func init() {
	registerLibrary("graph_gophers_graphql_go", func() {
		_, _ = graphql.ParseSchema("type Query { a: String }", nil)
	})
}
