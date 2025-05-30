// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package ext // import "github.com/DataDog/dd-trace-go/v2/ddtrace/ext"

// App types determine how to categorize a trace in the Datadog application.
// For more fine-grained behaviour, use the SpanType* constants.
const (
	// AppTypeDB specifies the DB span type and can be used as a tag value
	// for a span's SpanType tag. If possible, use one of the SpanType*
	// constants for a more accurate indication.
	AppTypeDB = "db"

	// AppTypeCache specifies the Cache span type and can be used as a tag value
	// for a span's SpanType tag. If possible, consider using SpanTypeRedis or
	// SpanTypeMemcached.
	AppTypeCache = "cache"

	// AppTypeRPC specifies the RPC span type and can be used as a tag value
	// for a span's SpanType tag.
	AppTypeRPC = "rpc"
)

// Span types have similar behaviour to "app types" and help categorize
// traces in the Datadog application. They can also help fine grain agent
// level behaviours such as obfuscation and quantization, when these are
// enabled in the agent's configuration.
const (
	// SpanTypeWeb marks a span as an HTTP server request.
	SpanTypeWeb = "web"

	// SpanTypeHTTP marks a span as an HTTP client request.
	SpanTypeHTTP = "http"

	// SpanTypeSQL marks a span as an SQL operation. These spans may
	// have an "sql.command" tag.
	SpanTypeSQL = "sql"

	// SpanTypeCassandra marks a span as a Cassandra operation. These
	// spans may have an "sql.command" tag.
	SpanTypeCassandra = "cassandra"

	// SpanTypeRedis marks a span as a Redis operation. These spans may
	// also have a "redis.raw_command" tag.
	SpanTypeRedis = "redis"

	// SpanTypeRedis marks a span as a Valkey operation.
	SpanTypeValkey = "valkey"

	// SpanTypeMemcached marks a span as a memcached operation.
	SpanTypeMemcached = "memcached"

	// SpanTypeMongoDB marks a span as a MongoDB operation.
	SpanTypeMongoDB = "mongodb"

	// SpanTypeElasticSearch marks a span as an ElasticSearch operation.
	// These spans may also have an "elasticsearch.body" tag.
	SpanTypeElasticSearch = "elasticsearch"

	// SpanTypeLevelDB marks a span as a leveldb operation
	SpanTypeLevelDB = "leveldb"

	// SpanTypeDNS marks a span as a DNS operation.
	SpanTypeDNS = "dns"

	// SpanTypeMessageConsumer marks a span as a queue operation
	SpanTypeMessageConsumer = "queue"

	// SpanTypeMessageProducer marks a span as a queue operation.
	SpanTypeMessageProducer = "queue"

	// SpanTypeConsul marks a span as a Consul operation.
	SpanTypeConsul = "consul"

	// SpanTypeGraphql marks a span as a graphql operation.
	SpanTypeGraphQL = "graphql"
)
