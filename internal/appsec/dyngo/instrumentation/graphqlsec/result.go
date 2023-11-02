// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package graphqlsec

// Result describes the result of a GraphQL operation.
type Result struct {
	// Data is the data returned from processing the GraphQL operation.
	Data any
	// Error is the error returned by processing the GraphQL Operation, if any.
	Error error
}
