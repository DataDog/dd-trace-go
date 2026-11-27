// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package otelc holds the otelc rules for graphql-go/graphql. See otelc.yaml.
package otelc

import (
	_ "unsafe" // for go:linkname

	"github.com/graphql-go/graphql"

	_ "github.com/DataDog/dd-trace-go/contrib/graphql-go/graphql/v2" // for go:linkname
)

// WrapSchema adds the tracing extension that the contrib's NewSchema adds to
// a schema that graphql.NewSchema returned, and returns err unchanged. The
// otelc.yaml rules call it around each graphql.NewSchema call.
func WrapSchema(schema graphql.Schema, err error) (graphql.Schema, error) {
	return wrapSchema(schema, err)
}

//go:linkname wrapSchema github.com/DataDog/dd-trace-go/contrib/graphql-go/graphql/v2.otelcWrapSchema
func wrapSchema(graphql.Schema, error) (graphql.Schema, error)
