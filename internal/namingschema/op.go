// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package namingschema

import (
	"fmt"
	"strings"
)

type Service int

const (
	// client/server
	HTTPClient Service = iota
	HTTPServer
	GRPCClient
	GRPCServer
	GraphqlServer
	TwirpClient
	TwirpServer

	// messaging
	KafkaOutbound
	KafkaInbound
	GCPPubSubInbound
	GCPPubSubOutbound

	// cache
	MemcachedOutbound
	RedisOutbound

	// db
	ElasticSearchOutbound
	MongoDBOutbound
	CassandraOutbound
	LevelDBOutbound
	BuntDBOutbound
	ConsulOutbound
)

func opV1(s Service) string {
	switch s {
	// Client/Server
	case HTTPClient:
		return "http.client.request"
	case HTTPServer:
		return "http.server.request"
	case GRPCClient:
		return "grpc.client.request"
	case GRPCServer:
		return "grpc.server.request"
	case GraphqlServer:
		return "graphql.server.request"
	case TwirpClient:
		return "twirp.client.request"
	case TwirpServer:
		return "twirp.server.request"

	// Messaging
	case KafkaOutbound:
		return "kafka.send"
	case KafkaInbound:
		return "kafka.process"
	case GCPPubSubInbound:
		return "gcp.pubsub.process"
	case GCPPubSubOutbound:
		return "gcp.pubsub.send"

	// Cache
	case MemcachedOutbound:
		return "memcached.command"
	case RedisOutbound:
		return "redis.command"

	// Database
	case ElasticSearchOutbound:
		return "elasticsearch.query"
	case MongoDBOutbound:
		return "mongodb.query"
	case CassandraOutbound:
		return "cassandra.query"
	case LevelDBOutbound:
		return "leveldb.query"
	case BuntDBOutbound:
		return "buntdb.query"
	case ConsulOutbound:
		return "consul.query"
	}
	return ""
}

func opV0(s Service) string {
	switch s {
	case HTTPClient, HTTPServer:
		return "http.request"
	case GRPCClient:
		return "grpc.client"
	case GRPCServer:
		return "grpc.server"
	case GraphqlServer:
		return "graphql.request"
	case TwirpClient:
		return "twirp.request"
	case TwirpServer:
		return "twirp.request"
	case KafkaOutbound:
		return "kafka.produce"
	case KafkaInbound:
		return "kafka.consume"
	case GCPPubSubInbound:
		return "pubsub.receive"
	case GCPPubSubOutbound:
		return "pubsub.publish"
	case MemcachedOutbound:
		return "memcached.query"
	case RedisOutbound:
		return "redis.command"
	case ElasticSearchOutbound:
		return "elasticsearch.query"
	case MongoDBOutbound:
		return "mongodb.query"
	case CassandraOutbound:
		return "cassandra.query"
	case LevelDBOutbound:
		return "leveldb.query"
	case BuntDBOutbound:
		return "buntdb.query"
	case ConsulOutbound:
		return "consul.command"
	}
	return ""
}

func OpName(s Service) string {
	switch GetVersion() {
	case SchemaV1:
		return opV1(s)
	default:
		return opV0(s)
	}
}

func OpNameOverrideV0(s Service, overrideV0 string) string {
	switch GetVersion() {
	case SchemaV1:
		return opV1(s)
	default:
		return overrideV0
	}
}

func DBOpName(system string, overrideV0 string) string {
	switch GetVersion() {
	case SchemaV1:
		return system + ".query"
	default:
		return overrideV0
	}
}

func isMessagingSendOp(awsService, awsOperation string) bool {
	s, op := strings.ToLower(awsService), strings.ToLower(awsOperation)
	if s == "sqs" {
		return strings.HasPrefix(op, "sendmessage")
	}
	if s == "sns" {
		return op == "publish"
	}
	return false
}

func AWSOpName(service, operation, overrideV0 string) string {
	switch GetVersion() {
	case SchemaV1:
		op := "request"
		if isMessagingSendOp(service, operation) {
			op = "send"
		}
		return fmt.Sprintf("aws.%s.%s", strings.ToLower(service), op)
	default:
		return overrideV0
	}
}
