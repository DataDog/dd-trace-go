// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package v2fix

import "testing"

func TestRewriteV1ImportPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "core package",
			in:   "gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer",
			want: "github.com/DataDog/dd-trace-go/v2/ddtrace/tracer",
		},
		{
			name: "contrib module root",
			in:   "gopkg.in/DataDog/dd-trace-go.v1/contrib/net/http",
			want: "github.com/DataDog/dd-trace-go/contrib/net/http/v2",
		},
		{
			name: "contrib subpackage",
			in:   "gopkg.in/DataDog/dd-trace-go.v1/contrib/net/http/client",
			want: "github.com/DataDog/dd-trace-go/contrib/net/http/v2/client",
		},
		{
			name: "contrib nested module root",
			in:   "gopkg.in/DataDog/dd-trace-go.v1/contrib/confluentinc/confluent-kafka-go/kafka",
			want: "github.com/DataDog/dd-trace-go/contrib/confluentinc/confluent-kafka-go/kafka/v2",
		},
		{
			name: "contrib nested module subpackage",
			in:   "gopkg.in/DataDog/dd-trace-go.v1/contrib/confluentinc/confluent-kafka-go/kafka/producer",
			want: "github.com/DataDog/dd-trace-go/contrib/confluentinc/confluent-kafka-go/kafka/v2/producer",
		},
		{
			name: "longest module prefix wins",
			in:   "gopkg.in/DataDog/dd-trace-go.v1/contrib/google.golang.org/api/internal/gen_endpoints/config",
			want: "github.com/DataDog/dd-trace-go/contrib/google.golang.org/api/internal/gen_endpoints/v2/config",
		},
		{
			name: "unknown contrib fallback",
			in:   "gopkg.in/DataDog/dd-trace-go.v1/contrib/acme/custom/pkg",
			want: "github.com/DataDog/dd-trace-go/contrib/acme/custom/pkg/v2",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := rewriteV1ImportPath(tt.in)
			if got != tt.want {
				t.Fatalf("rewriteV1ImportPath(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
