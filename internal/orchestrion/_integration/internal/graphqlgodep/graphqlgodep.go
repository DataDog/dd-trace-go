// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package graphqlgodep builds graphql-go schemas inside a dependency module.
package graphqlgodep

import "github.com/graphql-go/graphql"

// NewSchema returns a graphql-go schema for config.
func NewSchema(config graphql.SchemaConfig) (graphql.Schema, error) {
	return graphql.NewSchema(config)
}
