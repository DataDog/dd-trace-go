// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package graphgophersdep parses graph-gophers/graphql-go schemas inside a
// dependency module.
package graphgophersdep

import (
	"github.com/graph-gophers/graphql-go"
)

// ParseSchema returns the schema parsed from schemaString with resolver.
func ParseSchema(schemaString string, resolver any) (*graphql.Schema, error) {
	return graphql.ParseSchema(schemaString, resolver)
}
