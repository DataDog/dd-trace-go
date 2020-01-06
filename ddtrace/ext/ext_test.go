// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2020 Datadog, Inc.

package ext

import "testing"

// TestSpec asserts that the constants represented in this package match the
// ones that are expected by the rest of our pipeline.
func TestSpec(t *testing.T) {
	// tests holds pairs of tests where each i == i+1
	//
	// changing any of these should be considered a breaking change and
	// should require a major version release.
	tests := []string{
		AppTypeWeb, "web",
		AppTypeDB, "db",
		AppTypeCache, "cache",
		AppTypeRPC, "rpc",
		SpanTypeWeb, "web",
		SpanTypeHTTP, "http",
		SpanTypeSQL, "sql",
		SQLType, "sql",
		SpanTypeCassandra, "cassandra",
		SpanTypeRedis, "redis",
		SpanTypeElasticSearch, "elasticsearch",
		SQLQuery, "sql.query",
		HTTPURL, "http.url",
		Environment, "env",
	}
	if len(tests)%2 != 0 {
		t.Fatal("uneven test count")
	}
	for i := 0; i < len(tests); i += 2 {
		if tests[i] != tests[i+1] {
			t.Fatalf("changed %q", tests[i+1])
		}
	}
}
