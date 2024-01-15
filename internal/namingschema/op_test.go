// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package namingschema_test

import (
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/namingschema"

	"github.com/stretchr/testify/assert"
)

func TestOpName(t *testing.T) {
	optOverrideV0 := "override-v0"

	testCases := []struct {
		name      string
		newSchema func() string
		wantV0    string
		wantV1    string
	}{
		{
			name: "kafka outbound",
			newSchema: func() string {
				return namingschema.OpName(namingschema.KafkaOutbound)
			},
			wantV0: "kafka.produce",
			wantV1: "kafka.send",
		},
		{
			name: "kafka inbound",
			newSchema: func() string {
				return namingschema.OpName(namingschema.KafkaInbound)
			},
			wantV0: "kafka.consume",
			wantV1: "kafka.process",
		},
		{
			name: "gcp pubsub outbound",
			newSchema: func() string {
				return namingschema.OpName(namingschema.GCPPubSubOutbound)
			},
			wantV0: "pubsub.publish",
			wantV1: "gcp.pubsub.send",
		},
		{
			name: "gcp pubsub inbound",
			newSchema: func() string {
				return namingschema.OpName(namingschema.GCPPubSubInbound)
			},
			wantV0: "pubsub.receive",
			wantV1: "gcp.pubsub.process",
		},
		{
			name: "override",
			newSchema: func() string {
				return namingschema.OpNameOverrideV0(namingschema.GCPPubSubInbound, optOverrideV0)
			},
			wantV0: "override-v0",
			wantV1: "gcp.pubsub.process",
		},
		{
			name: "http client",
			newSchema: func() string {
				return namingschema.OpName(namingschema.HTTPClient)
			},
			wantV0: "http.request",
			wantV1: "http.client.request",
		},
		{
			name: "http server",
			newSchema: func() string {
				return namingschema.OpName(namingschema.HTTPServer)
			},
			wantV0: "http.request",
			wantV1: "http.server.request",
		},
		{
			name: "grpc client",
			newSchema: func() string {
				return namingschema.OpName(namingschema.GRPCClient)
			},
			wantV0: "grpc.client",
			wantV1: "grpc.client.request",
		},
		{
			name: "grpc server",
			newSchema: func() string {
				return namingschema.OpName(namingschema.GRPCServer)
			},
			wantV0: "grpc.server",
			wantV1: "grpc.server.request",
		},
		{
			name: "graphql server",
			newSchema: func() string {
				return namingschema.OpName(namingschema.GraphqlServer)
			},
			wantV0: "graphql.request",
			wantV1: "graphql.server.request",
		},
		{
			name: "memcached outbound",
			newSchema: func() string {
				return namingschema.OpName(namingschema.MemcachedOutbound)
			},
			wantV0: "memcached.query",
			wantV1: "memcached.command",
		},
		{
			name: "redis outbound",
			newSchema: func() string {
				return namingschema.OpName(namingschema.RedisOutbound)
			},
			wantV0: "redis.command",
			wantV1: "redis.command",
		},
		{
			name: "elasticsearch outbound",
			newSchema: func() string {
				return namingschema.OpName(namingschema.ElasticSearchOutbound)
			},
			wantV0: "elasticsearch.query",
			wantV1: "elasticsearch.query",
		},
		{
			name: "mongodb outbound",
			newSchema: func() string {
				return namingschema.OpName(namingschema.MongoDBOutbound)
			},
			wantV0: "mongodb.query",
			wantV1: "mongodb.query",
		},
		{
			name: "cassandra outbound",
			newSchema: func() string {
				return namingschema.OpName(namingschema.CassandraOutbound)
			},
			wantV0: "cassandra.query",
			wantV1: "cassandra.query",
		},
		{
			name: "DBOpName",
			newSchema: func() string {
				return namingschema.DBOpName("my-custom-database", optOverrideV0)
			},
			wantV0: "override-v0",
			wantV1: "my-custom-database.query",
		},
		{
			name: "AWSOpName",
			newSchema: func() string {
				return namingschema.AWSOpName("service", "operation", optOverrideV0)
			},
			wantV0: "override-v0",
			wantV1: "aws.service.request",
		},
		{
			name: "AWSOpName-sns-send",
			newSchema: func() string {
				return namingschema.AWSOpName("sns", "publish", optOverrideV0)
			},
			wantV0: "override-v0",
			wantV1: "aws.sns.send",
		},
		{
			name: "AWSOpName-sns-other",
			newSchema: func() string {
				return namingschema.AWSOpName("sns", "other", optOverrideV0)
			},
			wantV0: "override-v0",
			wantV1: "aws.sns.request",
		},
		{
			name: "AWSOpName-sqs-send",
			newSchema: func() string {
				return namingschema.AWSOpName("sqs", "sendmessage-something", optOverrideV0)
			},
			wantV0: "override-v0",
			wantV1: "aws.sqs.send",
		},
		{
			name: "AWSOpName-sqs-other",
			newSchema: func() string {
				return namingschema.AWSOpName("sqs", "other", optOverrideV0)
			},
			wantV0: "override-v0",
			wantV1: "aws.sqs.request",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			version := namingschema.GetVersion()
			defer namingschema.SetVersion(version)

			namingschema.SetVersion(namingschema.SchemaV0)
			assert.Equal(t, tc.wantV0, tc.newSchema())

			namingschema.SetVersion(namingschema.SchemaV1)
			assert.Equal(t, tc.wantV1, tc.newSchema())
		})
	}
}
