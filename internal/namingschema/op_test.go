// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package namingschema_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/namingschema"
)

func TestOpContribSchemas(t *testing.T) {
	optOverrideV0 := namingschema.WithVersionOverride(namingschema.SchemaV0, "override-v0")
	optOverrideV1 := namingschema.WithVersionOverride(namingschema.SchemaV1, "override-v1")

	testCases := []struct {
		name      string
		newSchema func() *namingschema.Schema
		wantV0    string
		wantV1    string
	}{
		{
			name: "kafka outbound",
			newSchema: func() *namingschema.Schema {
				return namingschema.NewKafkaOutboundOp()
			},
			wantV0: "kafka.produce",
			wantV1: "kafka.send",
		},
		{
			name: "kafka inbound",
			newSchema: func() *namingschema.Schema {
				return namingschema.NewKafkaInboundOp()
			},
			wantV0: "kafka.consume",
			wantV1: "kafka.process",
		},
		{
			name: "db outbound override",
			newSchema: func() *namingschema.Schema {
				return namingschema.NewDBOutboundOp("test", optOverrideV0, optOverrideV1)
			},
			wantV0: "override-v0",
			wantV1: "override-v1",
		},
		{
			name: "http client",
			newSchema: func() *namingschema.Schema {
				return namingschema.NewHTTPClientOp()
			},
			wantV0: "http.request",
			wantV1: "http.client.request",
		},
		{
			name: "http server",
			newSchema: func() *namingschema.Schema {
				return namingschema.NewHTTPServerOp()
			},
			wantV0: "http.request",
			wantV1: "http.server.request",
		},
		{
			name: "grpc client",
			newSchema: func() *namingschema.Schema {
				return namingschema.NewGRPCClientOp()
			},
			wantV0: "grpc.client",
			wantV1: "grpc.client.request",
		},
		{
			name: "grpc server",
			newSchema: func() *namingschema.Schema {
				return namingschema.NewGRPCServerOp()
			},
			wantV0: "grpc.server",
			wantV1: "grpc.server.request",
		},
		{
			name: "client outbound override",
			newSchema: func() *namingschema.Schema {
				return namingschema.NewClientOutboundOp("test", optOverrideV0, optOverrideV1)
			},
			wantV0: "override-v0",
			wantV1: "override-v1",
		},
		{
			name: "server inbound override",
			newSchema: func() *namingschema.Schema {
				return namingschema.NewServerInboundOp("test", optOverrideV0, optOverrideV1)
			},
			wantV0: "override-v0",
			wantV1: "override-v1",
		},
		{
			name: "memcached outbound",
			newSchema: func() *namingschema.Schema {
				return namingschema.NewMemcachedOutboundOp()
			},
			wantV0: "memcached.query",
			wantV1: "memcached.command",
		},
		{
			name: "cache outbound override",
			newSchema: func() *namingschema.Schema {
				return namingschema.NewCacheOutboundOp("test", optOverrideV0, optOverrideV1)
			},
			wantV0: "override-v0",
			wantV1: "override-v1",
		},
		{
			name: "elasticsearch outbound",
			newSchema: func() *namingschema.Schema {
				return namingschema.NewElasticsearchOutboundOp()
			},
			wantV0: "elasticsearch.query",
			wantV1: "elasticsearch.query",
		},
		{
			name: "db outbound override",
			newSchema: func() *namingschema.Schema {
				return namingschema.NewDBOutboundOp("test", optOverrideV0, optOverrideV1)
			},
			wantV0: "override-v0",
			wantV1: "override-v1",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			version := namingschema.GetVersion()
			defer namingschema.SetVersion(version)

			namingschema.SetVersion(namingschema.SchemaV0)
			assert.Equal(t, tc.wantV0, tc.newSchema().GetName())

			namingschema.SetVersion(namingschema.SchemaV1)
			assert.Equal(t, tc.wantV1, tc.newSchema().GetName())
		})
	}
}
