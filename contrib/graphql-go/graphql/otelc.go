// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package graphql

import "github.com/graphql-go/graphql"

// otelcWrapSchema adds the extension that NewSchema adds to a schema that
// graphql.NewSchema returned. graphql.NewSchema sets the extensions only when
// it returns no error, so the schema is kept as is on error. The otelc rules
// (./otelc/otelc.yaml) reach it through go:linkname.
func otelcWrapSchema(schema graphql.Schema, err error) (graphql.Schema, error) {
	if err != nil {
		return schema, err
	}
	schema.AddExtensions(newExtension())
	return schema, nil
}
