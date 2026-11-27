// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package main

import "github.com/graphql-go/graphql"

func init() {
	registerLibrary("graphql_go", func() {
		_, _ = graphql.NewSchema(graphql.SchemaConfig{
			Query: graphql.NewObject(graphql.ObjectConfig{
				Name:   "Query",
				Fields: graphql.Fields{"ok": &graphql.Field{Type: graphql.Boolean}},
			}),
		})
	})
}
