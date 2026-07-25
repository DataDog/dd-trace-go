// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package config

import (
	"errors"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/DataDog/dd-trace-go/v2/internal/config/provider"
	"github.com/DataDog/dd-trace-go/v2/internal/config/schema"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
)

var (
	instrumentationReporter   Reporter
	instrumentationGeneration atomic.Uint64
	newEnvironmentProvider    = provider.NewEnvironment
	newStableProvider         = sync.OnceValue(provider.New)
	instrumentationRawIndex   struct {
		once  sync.Once
		byKey map[string]RawDefinition
	}
)

var httpTraceBinding = ConsumerBinding{
	ID:       "instrumentation.httptrace",
	Consumer: "instrumentation/httptrace",
	Keys: []string{
		"DD_GOOGLE_CLOUD_PUBSUB_PROPAGATION_AS_SPAN_LINKS",
		"DD_TRACE_BAGGAGE_TAG_KEYS",
		"DD_TRACE_CLIENT_IP_ENABLED",
		"DD_TRACE_HTTP_SERVER_ERROR_STATUSES",
		"DD_TRACE_HTTP_URL_QUERY_STRING_ALLOWLIST",
		"DD_TRACE_HTTP_URL_QUERY_STRING_ALLOWLIST_CLIENT",
		"DD_TRACE_HTTP_URL_QUERY_STRING_ALLOWLIST_SERVER",
		"DD_TRACE_HTTP_URL_QUERY_STRING_DISABLED",
		"DD_TRACE_INFERRED_PROXY_SERVICES_ENABLED",
		"DD_TRACE_OBFUSCATION_QUERY_STRING_REGEXP",
		"DD_TRACE_RESOURCE_RENAMING_ALWAYS_SIMPLIFIED_ENDPOINT",
		"DD_TRACE_RESOURCE_RENAMING_ENABLED",
	},
	Sampling:        SampleConstructor,
	EnvironmentOnly: true,
}

var propagationBinding = ConsumerBinding{
	ID:       "tracer.public-propagator",
	Consumer: "ddtrace/tracer.NewPropagator",
	Keys: []string{
		"DD_TRACE_PROPAGATION_BEHAVIOR_EXTRACT",
		"DD_TRACE_PROPAGATION_EXTRACT_FIRST",
		"DD_TRACE_PROPAGATION_STYLE",
		"DD_TRACE_PROPAGATION_STYLE_EXTRACT",
		"DD_TRACE_PROPAGATION_STYLE_INJECT",
		"OTEL_PROPAGATORS",
	},
	Sampling:        SampleConstructor,
	EnvironmentOnly: true,
}

var tracerOTelBindings = map[string]ConsumerBinding{
	"service": {
		ID: "tracer.otel.service", Consumer: "ddtrace/tracer OTel compatibility",
		Keys: []string{"DD_SERVICE", "OTEL_SERVICE_NAME"}, Sampling: SampleConstructor,
		EnvironmentOnly: true,
	},
	"debugMode": {
		ID: "tracer.otel.debug", Consumer: "ddtrace/tracer OTel compatibility",
		Keys: []string{"DD_TRACE_DEBUG", "OTEL_LOG_LEVEL"}, Sampling: SampleConstructor,
	},
	"enabled": {
		ID: "tracer.otel.enabled", Consumer: "ddtrace/tracer OTel compatibility",
		Keys: []string{"DD_TRACE_ENABLED", "OTEL_TRACES_EXPORTER"}, Sampling: SampleConstructor,
		EnvironmentOnly: true,
	},
	"sampleRate": {
		ID: "tracer.otel.sample-rate", Consumer: "ddtrace/tracer OTel compatibility",
		Keys: []string{"DD_TRACE_SAMPLE_RATE", "OTEL_TRACES_SAMPLER", "OTEL_TRACES_SAMPLER_ARG"}, Sampling: SampleConstructor,
		EnvironmentOnly: true,
	},
	"propagationStyle": propagationBinding,
	"resourceAttributes": {
		ID: "tracer.otel.resource-attributes", Consumer: "ddtrace/tracer OTel compatibility",
		Keys: []string{"DD_TAGS", "OTEL_RESOURCE_ATTRIBUTES"}, Sampling: SampleConstructor,
		EnvironmentOnly: true,
	},
}

var tracerOTelDDKeys = map[string]string{
	"service":            "DD_SERVICE",
	"debugMode":          "DD_TRACE_DEBUG",
	"enabled":            "DD_TRACE_ENABLED",
	"sampleRate":         "DD_TRACE_SAMPLE_RATE",
	"propagationStyle":   "DD_TRACE_PROPAGATION_STYLE",
	"resourceAttributes": "DD_TAGS",
}

var integrationAnalyticsKeys = []string{
	"DD_TRACE_AEROSPIKE_ANALYTICS_ENABLED",
	"DD_TRACE_AWS_ANALYTICS_ENABLED",
	"DD_TRACE_BUNTDB_ANALYTICS_ENABLED",
	"DD_TRACE_CHI_ANALYTICS_ENABLED",
	"DD_TRACE_CONSUL_ANALYTICS_ENABLED",
	"DD_TRACE_ECHO_ANALYTICS_ENABLED",
	"DD_TRACE_ELASTIC_ANALYTICS_ENABLED",
	"DD_TRACE_FASTHTTP_ANALYTICS_ENABLED",
	"DD_TRACE_FIBER_ANALYTICS_ENABLED",
	"DD_TRACE_GCP_PUBSUB_ANALYTICS_ENABLED",
	"DD_TRACE_GIN_ANALYTICS_ENABLED",
	"DD_TRACE_GOCQL_ANALYTICS_ENABLED",
	"DD_TRACE_GOJI_ANALYTICS_ENABLED",
	"DD_TRACE_GOOGLE_API_ANALYTICS_ENABLED",
	"DD_TRACE_GOPG_ANALYTICS_ENABLED",
	"DD_TRACE_GQLGEN_ANALYTICS_ENABLED",
	"DD_TRACE_GRAPHQL_ANALYTICS_ENABLED",
	"DD_TRACE_GRPC_ANALYTICS_ENABLED",
	"DD_TRACE_HTTP_ANALYTICS_ENABLED",
	"DD_TRACE_HTTPROUTER_ANALYTICS_ENABLED",
	"DD_TRACE_HTTPTREEMUX_ANALYTICS_ENABLED",
	"DD_TRACE_KAFKA_ANALYTICS_ENABLED",
	"DD_TRACE_LAMBDA_ANALYTICS_ENABLED",
	"DD_TRACE_LEVELDB_ANALYTICS_ENABLED",
	"DD_TRACE_LOGRUS_ANALYTICS_ENABLED",
	"DD_TRACE_MCP_ANALYTICS_ENABLED",
	"DD_TRACE_MEMCACHE_ANALYTICS_ENABLED",
	"DD_TRACE_MGO_ANALYTICS_ENABLED",
	"DD_TRACE_MONGO_ANALYTICS_ENABLED",
	"DD_TRACE_MUX_ANALYTICS_ENABLED",
	"DD_TRACE_NEGRONI_ANALYTICS_ENABLED",
	"DD_TRACE_REDIGO_ANALYTICS_ENABLED",
	"DD_TRACE_REDIS_ANALYTICS_ENABLED",
	"DD_TRACE_RESTFUL_ANALYTICS_ENABLED",
	"DD_TRACE_SARAMA_ANALYTICS_ENABLED",
	"DD_TRACE_SQL_ANALYTICS_ENABLED",
	"DD_TRACE_TWIRP_ANALYTICS_ENABLED",
	"DD_TRACE_VALKEY_ANALYTICS_ENABLED",
	"DD_TRACE_VAULT_ANALYTICS_ENABLED",
	"DD_TRACE_ZAP_ANALYTICS_ENABLED",
	"DD_TRACE_ZEROLOG_ANALYTICS_ENABLED",
}

var integrationAnalyticsBindings map[string]ConsumerBinding

func init() {
	registerRaw(RawDefinition{Key: "DD_API_SECURITY_ENDPOINT_COLLECTION_ENABLED", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_GOOGLE_CLOUD_PUBSUB_PROPAGATION_AS_SPAN_LINKS", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_LLMOBS_AGENTLESS_ENABLED", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_LLMOBS_ENABLED", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_LLMOBS_ML_APP", Sources: SourceEnvironment, Telemetry: TelemetryOmit})
	registerRaw(RawDefinition{Key: "DD_LLMOBS_PROJECT_NAME", Sources: SourceEnvironment, Telemetry: TelemetryOmit})
	registerRaw(RawDefinition{Key: "DD_TRACE_128_BIT_TRACEID_LOGGING_ENABLED", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_BAGGAGE_TAG_KEYS", Sources: SourceEnvironment, Telemetry: TelemetryOmit})
	registerRaw(RawDefinition{Key: "DD_TRACE_CLIENT_IP_ENABLED", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_DEBUG_SEELOG_WORKAROUND", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_GRAPHQL_ERROR_EXTENSIONS", Sources: SourceEnvironment, Telemetry: TelemetryOmit})
	registerRaw(RawDefinition{Key: "DD_TRACE_HTTP_SERVER_ERROR_STATUSES", Sources: SourceEnvironment, Telemetry: TelemetryOmit})
	registerRaw(RawDefinition{Key: "DD_TRACE_HTTP_URL_QUERY_STRING_ALLOWLIST", Sources: SourceEnvironment, Telemetry: TelemetryOmit})
	registerRaw(RawDefinition{Key: "DD_TRACE_HTTP_URL_QUERY_STRING_ALLOWLIST_CLIENT", Sources: SourceEnvironment, Telemetry: TelemetryOmit})
	registerRaw(RawDefinition{Key: "DD_TRACE_HTTP_URL_QUERY_STRING_ALLOWLIST_SERVER", Sources: SourceEnvironment, Telemetry: TelemetryOmit})
	registerRaw(RawDefinition{Key: "DD_TRACE_HTTP_URL_QUERY_STRING_DISABLED", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_INFERRED_PROXY_SERVICES_ENABLED", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_OBFUSCATION_QUERY_STRING_REGEXP", Sources: SourceEnvironment, Telemetry: TelemetryOmit})
	registerRaw(RawDefinition{Key: "DD_TRACE_PROPAGATION_STYLE", Sources: SourceStable, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_REMOVE_INTEGRATION_SERVICE_NAMES_ENABLED", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_RESOURCE_RENAMING_ALWAYS_SIMPLIFIED_ENDPOINT", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_RESOURCE_RENAMING_ENABLED", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "OTEL_LOG_LEVEL", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "OTEL_PROPAGATORS", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "OTEL_RESOURCE_ATTRIBUTES", Sources: SourceEnvironment, Telemetry: TelemetryOmit})
	registerRaw(RawDefinition{Key: "OTEL_SERVICE_NAME", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "OTEL_TRACES_SAMPLER", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "OTEL_TRACES_SAMPLER_ARG", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_AEROSPIKE_ANALYTICS_ENABLED", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_AWS_ANALYTICS_ENABLED", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_BUNTDB_ANALYTICS_ENABLED", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_CHI_ANALYTICS_ENABLED", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_CONSUL_ANALYTICS_ENABLED", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_ECHO_ANALYTICS_ENABLED", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_ELASTIC_ANALYTICS_ENABLED", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_FASTHTTP_ANALYTICS_ENABLED", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_FIBER_ANALYTICS_ENABLED", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_GCP_PUBSUB_ANALYTICS_ENABLED", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_GIN_ANALYTICS_ENABLED", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_GOCQL_ANALYTICS_ENABLED", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_GOJI_ANALYTICS_ENABLED", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_GOOGLE_API_ANALYTICS_ENABLED", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_GOPG_ANALYTICS_ENABLED", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_GQLGEN_ANALYTICS_ENABLED", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_GRAPHQL_ANALYTICS_ENABLED", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_GRPC_ANALYTICS_ENABLED", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_HTTP_ANALYTICS_ENABLED", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_HTTPROUTER_ANALYTICS_ENABLED", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_HTTPTREEMUX_ANALYTICS_ENABLED", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_KAFKA_ANALYTICS_ENABLED", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_LAMBDA_ANALYTICS_ENABLED", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_LEVELDB_ANALYTICS_ENABLED", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_LOGRUS_ANALYTICS_ENABLED", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_MCP_ANALYTICS_ENABLED", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_MEMCACHE_ANALYTICS_ENABLED", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_MGO_ANALYTICS_ENABLED", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_MONGO_ANALYTICS_ENABLED", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_MUX_ANALYTICS_ENABLED", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_NEGRONI_ANALYTICS_ENABLED", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_REDIGO_ANALYTICS_ENABLED", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_REDIS_ANALYTICS_ENABLED", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_RESTFUL_ANALYTICS_ENABLED", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_SARAMA_ANALYTICS_ENABLED", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_SQL_ANALYTICS_ENABLED", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_TWIRP_ANALYTICS_ENABLED", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_VALKEY_ANALYTICS_ENABLED", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_VAULT_ANALYTICS_ENABLED", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_ZAP_ANALYTICS_ENABLED", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_ZEROLOG_ANALYTICS_ENABLED", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerBinding(ConsumerBinding{
		ID: "instrumentation.httptrace", Consumer: "instrumentation/httptrace",
		Keys: []string{
			"DD_GOOGLE_CLOUD_PUBSUB_PROPAGATION_AS_SPAN_LINKS",
			"DD_TRACE_BAGGAGE_TAG_KEYS",
			"DD_TRACE_CLIENT_IP_ENABLED",
			"DD_TRACE_HTTP_SERVER_ERROR_STATUSES",
			"DD_TRACE_HTTP_URL_QUERY_STRING_ALLOWLIST",
			"DD_TRACE_HTTP_URL_QUERY_STRING_ALLOWLIST_CLIENT",
			"DD_TRACE_HTTP_URL_QUERY_STRING_ALLOWLIST_SERVER",
			"DD_TRACE_HTTP_URL_QUERY_STRING_DISABLED",
			"DD_TRACE_INFERRED_PROXY_SERVICES_ENABLED",
			"DD_TRACE_OBFUSCATION_QUERY_STRING_REGEXP",
			"DD_TRACE_RESOURCE_RENAMING_ALWAYS_SIMPLIFIED_ENDPOINT",
			"DD_TRACE_RESOURCE_RENAMING_ENABLED",
		},
		Sampling: SampleConstructor, EnvironmentOnly: true,
	})
	registerBinding(ConsumerBinding{
		ID: "tracer.public-propagator", Consumer: "ddtrace/tracer.NewPropagator",
		Keys: []string{
			"DD_TRACE_PROPAGATION_BEHAVIOR_EXTRACT",
			"DD_TRACE_PROPAGATION_EXTRACT_FIRST",
			"DD_TRACE_PROPAGATION_STYLE",
			"DD_TRACE_PROPAGATION_STYLE_EXTRACT",
			"DD_TRACE_PROPAGATION_STYLE_INJECT",
			"OTEL_PROPAGATORS",
		},
		Sampling: SampleConstructor, EnvironmentOnly: true,
	})
	registerBinding(ConsumerBinding{
		ID: "tracer.trace-id-logging", Consumer: "ddtrace/tracer.Span.Format",
		Keys: []string{"DD_TRACE_128_BIT_TRACEID_LOGGING_ENABLED"}, Sampling: SamplePerCall,
		EnvironmentOnly: true,
	})
	registerBinding(ConsumerBinding{
		ID: "tracer.stopped-tags", Consumer: "ddtrace/tracer.Span.Format",
		Keys: []string{"DD_ENV", "DD_VERSION"}, Sampling: SamplePerCall,
		EnvironmentOnly: true,
	})
	registerBinding(ConsumerBinding{
		ID: "tracer.trace-id-generation-init", Consumer: "ddtrace/tracer package init",
		Keys: []string{"DD_TRACE_128_BIT_TRACEID_GENERATION_ENABLED"}, Sampling: SamplePackageInit,
		EnvironmentOnly: true,
	})
	registerBinding(ConsumerBinding{
		ID: "tracer.seelog-init", Consumer: "ddtrace/tracer package init",
		Keys: []string{"DD_TRACE_DEBUG_SEELOG_WORKAROUND"}, Sampling: SamplePackageInit,
		EnvironmentOnly: true,
	})
	registerBinding(ConsumerBinding{
		ID: "tracer.llmobs", Consumer: "ddtrace/tracer",
		Keys:     []string{"DD_LLMOBS_ENABLED", "DD_LLMOBS_ML_APP", "DD_LLMOBS_AGENTLESS_ENABLED", "DD_LLMOBS_PROJECT_NAME"},
		Sampling: SampleTracerConstruction, EnvironmentOnly: true,
	})
	registerBinding(ConsumerBinding{
		ID: "tracer.apm-tracing", Consumer: "ddtrace/tracer",
		Keys: []string{"DD_APM_TRACING_ENABLED"}, Sampling: SampleTracerConstruction,
	})
	registerBinding(ConsumerBinding{
		ID: "instrumentation.graphql-error-extensions", Consumer: "instrumentation/graphql",
		Keys: []string{"DD_TRACE_GRAPHQL_ERROR_EXTENSIONS"}, Sampling: SampleConstructor,
		EnvironmentOnly: true,
	})
	registerBinding(ConsumerBinding{
		ID: "instrumentation.naming-init", Consumer: "instrumentation/internal/namingschema",
		Keys:     []string{"DD_TRACE_SPAN_ATTRIBUTE_SCHEMA", "DD_TRACE_REMOVE_INTEGRATION_SERVICE_NAMES_ENABLED"},
		Sampling: SamplePackageInit, EnvironmentOnly: true,
	})
	registerBinding(ConsumerBinding{
		ID: "tracer.naming", Consumer: "internal/namingschema",
		Keys:     []string{"DD_TRACE_SPAN_ATTRIBUTE_SCHEMA", "DD_TRACE_REMOVE_INTEGRATION_SERVICE_NAMES_ENABLED"},
		Sampling: SampleTracerConstruction, EnvironmentOnly: true,
	})
	registerBinding(ConsumerBinding{
		ID: "naming.process-init", Consumer: "internal/namingschema process initialization",
		Keys:     []string{"DD_TRACE_SPAN_ATTRIBUTE_SCHEMA", "DD_TRACE_REMOVE_INTEGRATION_SERVICE_NAMES_ENABLED"},
		Sampling: SamplePackageInit, EnvironmentOnly: true,
	})
	registerBinding(ConsumerBinding{
		ID: "naming.service-reload", Consumer: "naming schema test reload",
		Keys: []string{"DD_SERVICE"}, Sampling: SampleConstructor, EnvironmentOnly: true,
	})
	registerBinding(ConsumerBinding{
		ID: "instrumentation.api-security-endpoint-collection", Consumer: "instrumentation",
		Keys: []string{"DD_API_SECURITY_ENDPOINT_COLLECTION_ENABLED"}, Sampling: SampleConstructor,
		EnvironmentOnly: true,
	})
	registerBinding(ConsumerBinding{
		ID: "instrumentation.data-streams", Consumer: "instrumentation",
		Keys: []string{"DD_DATA_STREAMS_ENABLED"}, Sampling: SampleConstructor,
	})
	registerBinding(ConsumerBinding{
		ID: "instrumentation.pubsub-span-links", Consumer: "contrib/cloud.google.com/go/pubsubtrace",
		Keys: []string{"DD_GOOGLE_CLOUD_PUBSUB_PROPAGATION_AS_SPAN_LINKS"}, Sampling: SampleConstructor,
		EnvironmentOnly: true,
	})
	registerBinding(ConsumerBinding{
		ID: "tracer.otel.service", Consumer: "ddtrace/tracer OTel compatibility",
		Keys: []string{"DD_SERVICE", "OTEL_SERVICE_NAME"}, Sampling: SampleConstructor,
		EnvironmentOnly: true,
	})
	registerBinding(ConsumerBinding{
		ID: "tracer.otel.debug", Consumer: "ddtrace/tracer OTel compatibility",
		Keys: []string{"DD_TRACE_DEBUG", "OTEL_LOG_LEVEL"}, Sampling: SampleConstructor,
	})
	registerBinding(ConsumerBinding{
		ID: "tracer.otel.enabled", Consumer: "ddtrace/tracer OTel compatibility",
		Keys: []string{"DD_TRACE_ENABLED", "OTEL_TRACES_EXPORTER"}, Sampling: SampleConstructor,
		EnvironmentOnly: true,
	})
	registerBinding(ConsumerBinding{
		ID: "tracer.otel.sample-rate", Consumer: "ddtrace/tracer OTel compatibility",
		Keys: []string{"DD_TRACE_SAMPLE_RATE", "OTEL_TRACES_SAMPLER", "OTEL_TRACES_SAMPLER_ARG"}, Sampling: SampleConstructor,
		EnvironmentOnly: true,
	})
	registerBinding(ConsumerBinding{
		ID: "tracer.otel.resource-attributes", Consumer: "ddtrace/tracer OTel compatibility",
		Keys: []string{"DD_TAGS", "OTEL_RESOURCE_ATTRIBUTES"}, Sampling: SampleConstructor,
		EnvironmentOnly: true,
	})
	registerBinding(ConsumerBinding{ID: "instrumentation.analytics.DD_TRACE_AEROSPIKE_ANALYTICS_ENABLED", Consumer: "instrumentation.AnalyticsRate", Keys: []string{"DD_TRACE_AEROSPIKE_ANALYTICS_ENABLED"}, Sampling: SampleConstructor, EnvironmentOnly: true})
	registerBinding(ConsumerBinding{ID: "instrumentation.analytics.DD_TRACE_AWS_ANALYTICS_ENABLED", Consumer: "instrumentation.AnalyticsRate", Keys: []string{"DD_TRACE_AWS_ANALYTICS_ENABLED"}, Sampling: SampleConstructor, EnvironmentOnly: true})
	registerBinding(ConsumerBinding{ID: "instrumentation.analytics.DD_TRACE_BUNTDB_ANALYTICS_ENABLED", Consumer: "instrumentation.AnalyticsRate", Keys: []string{"DD_TRACE_BUNTDB_ANALYTICS_ENABLED"}, Sampling: SampleConstructor, EnvironmentOnly: true})
	registerBinding(ConsumerBinding{ID: "instrumentation.analytics.DD_TRACE_CHI_ANALYTICS_ENABLED", Consumer: "instrumentation.AnalyticsRate", Keys: []string{"DD_TRACE_CHI_ANALYTICS_ENABLED"}, Sampling: SampleConstructor, EnvironmentOnly: true})
	registerBinding(ConsumerBinding{ID: "instrumentation.analytics.DD_TRACE_CONSUL_ANALYTICS_ENABLED", Consumer: "instrumentation.AnalyticsRate", Keys: []string{"DD_TRACE_CONSUL_ANALYTICS_ENABLED"}, Sampling: SampleConstructor, EnvironmentOnly: true})
	registerBinding(ConsumerBinding{ID: "instrumentation.analytics.DD_TRACE_ECHO_ANALYTICS_ENABLED", Consumer: "instrumentation.AnalyticsRate", Keys: []string{"DD_TRACE_ECHO_ANALYTICS_ENABLED"}, Sampling: SampleConstructor, EnvironmentOnly: true})
	registerBinding(ConsumerBinding{ID: "instrumentation.analytics.DD_TRACE_ELASTIC_ANALYTICS_ENABLED", Consumer: "instrumentation.AnalyticsRate", Keys: []string{"DD_TRACE_ELASTIC_ANALYTICS_ENABLED"}, Sampling: SampleConstructor, EnvironmentOnly: true})
	registerBinding(ConsumerBinding{ID: "instrumentation.analytics.DD_TRACE_FASTHTTP_ANALYTICS_ENABLED", Consumer: "instrumentation.AnalyticsRate", Keys: []string{"DD_TRACE_FASTHTTP_ANALYTICS_ENABLED"}, Sampling: SampleConstructor, EnvironmentOnly: true})
	registerBinding(ConsumerBinding{ID: "instrumentation.analytics.DD_TRACE_FIBER_ANALYTICS_ENABLED", Consumer: "instrumentation.AnalyticsRate", Keys: []string{"DD_TRACE_FIBER_ANALYTICS_ENABLED"}, Sampling: SampleConstructor, EnvironmentOnly: true})
	registerBinding(ConsumerBinding{ID: "instrumentation.analytics.DD_TRACE_GCP_PUBSUB_ANALYTICS_ENABLED", Consumer: "instrumentation.AnalyticsRate", Keys: []string{"DD_TRACE_GCP_PUBSUB_ANALYTICS_ENABLED"}, Sampling: SampleConstructor, EnvironmentOnly: true})
	registerBinding(ConsumerBinding{ID: "instrumentation.analytics.DD_TRACE_GIN_ANALYTICS_ENABLED", Consumer: "instrumentation.AnalyticsRate", Keys: []string{"DD_TRACE_GIN_ANALYTICS_ENABLED"}, Sampling: SampleConstructor, EnvironmentOnly: true})
	registerBinding(ConsumerBinding{ID: "instrumentation.analytics.DD_TRACE_GOCQL_ANALYTICS_ENABLED", Consumer: "instrumentation.AnalyticsRate", Keys: []string{"DD_TRACE_GOCQL_ANALYTICS_ENABLED"}, Sampling: SampleConstructor, EnvironmentOnly: true})
	registerBinding(ConsumerBinding{ID: "instrumentation.analytics.DD_TRACE_GOJI_ANALYTICS_ENABLED", Consumer: "instrumentation.AnalyticsRate", Keys: []string{"DD_TRACE_GOJI_ANALYTICS_ENABLED"}, Sampling: SampleConstructor, EnvironmentOnly: true})
	registerBinding(ConsumerBinding{ID: "instrumentation.analytics.DD_TRACE_GOOGLE_API_ANALYTICS_ENABLED", Consumer: "instrumentation.AnalyticsRate", Keys: []string{"DD_TRACE_GOOGLE_API_ANALYTICS_ENABLED"}, Sampling: SampleConstructor, EnvironmentOnly: true})
	registerBinding(ConsumerBinding{ID: "instrumentation.analytics.DD_TRACE_GOPG_ANALYTICS_ENABLED", Consumer: "instrumentation.AnalyticsRate", Keys: []string{"DD_TRACE_GOPG_ANALYTICS_ENABLED"}, Sampling: SampleConstructor, EnvironmentOnly: true})
	registerBinding(ConsumerBinding{ID: "instrumentation.analytics.DD_TRACE_GQLGEN_ANALYTICS_ENABLED", Consumer: "instrumentation.AnalyticsRate", Keys: []string{"DD_TRACE_GQLGEN_ANALYTICS_ENABLED"}, Sampling: SampleConstructor, EnvironmentOnly: true})
	registerBinding(ConsumerBinding{ID: "instrumentation.analytics.DD_TRACE_GRAPHQL_ANALYTICS_ENABLED", Consumer: "instrumentation.AnalyticsRate", Keys: []string{"DD_TRACE_GRAPHQL_ANALYTICS_ENABLED"}, Sampling: SampleConstructor, EnvironmentOnly: true})
	registerBinding(ConsumerBinding{ID: "instrumentation.analytics.DD_TRACE_GRPC_ANALYTICS_ENABLED", Consumer: "instrumentation.AnalyticsRate", Keys: []string{"DD_TRACE_GRPC_ANALYTICS_ENABLED"}, Sampling: SampleConstructor, EnvironmentOnly: true})
	registerBinding(ConsumerBinding{ID: "instrumentation.analytics.DD_TRACE_HTTP_ANALYTICS_ENABLED", Consumer: "instrumentation.AnalyticsRate", Keys: []string{"DD_TRACE_HTTP_ANALYTICS_ENABLED"}, Sampling: SampleConstructor, EnvironmentOnly: true})
	registerBinding(ConsumerBinding{ID: "instrumentation.analytics.DD_TRACE_HTTPROUTER_ANALYTICS_ENABLED", Consumer: "instrumentation.AnalyticsRate", Keys: []string{"DD_TRACE_HTTPROUTER_ANALYTICS_ENABLED"}, Sampling: SampleConstructor, EnvironmentOnly: true})
	registerBinding(ConsumerBinding{ID: "instrumentation.analytics.DD_TRACE_HTTPTREEMUX_ANALYTICS_ENABLED", Consumer: "instrumentation.AnalyticsRate", Keys: []string{"DD_TRACE_HTTPTREEMUX_ANALYTICS_ENABLED"}, Sampling: SampleConstructor, EnvironmentOnly: true})
	registerBinding(ConsumerBinding{ID: "instrumentation.analytics.DD_TRACE_KAFKA_ANALYTICS_ENABLED", Consumer: "instrumentation.AnalyticsRate", Keys: []string{"DD_TRACE_KAFKA_ANALYTICS_ENABLED"}, Sampling: SampleConstructor, EnvironmentOnly: true})
	registerBinding(ConsumerBinding{ID: "instrumentation.analytics.DD_TRACE_LAMBDA_ANALYTICS_ENABLED", Consumer: "instrumentation.AnalyticsRate", Keys: []string{"DD_TRACE_LAMBDA_ANALYTICS_ENABLED"}, Sampling: SampleConstructor, EnvironmentOnly: true})
	registerBinding(ConsumerBinding{ID: "instrumentation.analytics.DD_TRACE_LEVELDB_ANALYTICS_ENABLED", Consumer: "instrumentation.AnalyticsRate", Keys: []string{"DD_TRACE_LEVELDB_ANALYTICS_ENABLED"}, Sampling: SampleConstructor, EnvironmentOnly: true})
	registerBinding(ConsumerBinding{ID: "instrumentation.analytics.DD_TRACE_LOGRUS_ANALYTICS_ENABLED", Consumer: "instrumentation.AnalyticsRate", Keys: []string{"DD_TRACE_LOGRUS_ANALYTICS_ENABLED"}, Sampling: SampleConstructor, EnvironmentOnly: true})
	registerBinding(ConsumerBinding{ID: "instrumentation.analytics.DD_TRACE_MCP_ANALYTICS_ENABLED", Consumer: "instrumentation.AnalyticsRate", Keys: []string{"DD_TRACE_MCP_ANALYTICS_ENABLED"}, Sampling: SampleConstructor, EnvironmentOnly: true})
	registerBinding(ConsumerBinding{ID: "instrumentation.analytics.DD_TRACE_MEMCACHE_ANALYTICS_ENABLED", Consumer: "instrumentation.AnalyticsRate", Keys: []string{"DD_TRACE_MEMCACHE_ANALYTICS_ENABLED"}, Sampling: SampleConstructor, EnvironmentOnly: true})
	registerBinding(ConsumerBinding{ID: "instrumentation.analytics.DD_TRACE_MGO_ANALYTICS_ENABLED", Consumer: "instrumentation.AnalyticsRate", Keys: []string{"DD_TRACE_MGO_ANALYTICS_ENABLED"}, Sampling: SampleConstructor, EnvironmentOnly: true})
	registerBinding(ConsumerBinding{ID: "instrumentation.analytics.DD_TRACE_MONGO_ANALYTICS_ENABLED", Consumer: "instrumentation.AnalyticsRate", Keys: []string{"DD_TRACE_MONGO_ANALYTICS_ENABLED"}, Sampling: SampleConstructor, EnvironmentOnly: true})
	registerBinding(ConsumerBinding{ID: "instrumentation.analytics.DD_TRACE_MUX_ANALYTICS_ENABLED", Consumer: "instrumentation.AnalyticsRate", Keys: []string{"DD_TRACE_MUX_ANALYTICS_ENABLED"}, Sampling: SampleConstructor, EnvironmentOnly: true})
	registerBinding(ConsumerBinding{ID: "instrumentation.analytics.DD_TRACE_NEGRONI_ANALYTICS_ENABLED", Consumer: "instrumentation.AnalyticsRate", Keys: []string{"DD_TRACE_NEGRONI_ANALYTICS_ENABLED"}, Sampling: SampleConstructor, EnvironmentOnly: true})
	registerBinding(ConsumerBinding{ID: "instrumentation.analytics.DD_TRACE_REDIGO_ANALYTICS_ENABLED", Consumer: "instrumentation.AnalyticsRate", Keys: []string{"DD_TRACE_REDIGO_ANALYTICS_ENABLED"}, Sampling: SampleConstructor, EnvironmentOnly: true})
	registerBinding(ConsumerBinding{ID: "instrumentation.analytics.DD_TRACE_REDIS_ANALYTICS_ENABLED", Consumer: "instrumentation.AnalyticsRate", Keys: []string{"DD_TRACE_REDIS_ANALYTICS_ENABLED"}, Sampling: SampleConstructor, EnvironmentOnly: true})
	registerBinding(ConsumerBinding{ID: "instrumentation.analytics.DD_TRACE_RESTFUL_ANALYTICS_ENABLED", Consumer: "instrumentation.AnalyticsRate", Keys: []string{"DD_TRACE_RESTFUL_ANALYTICS_ENABLED"}, Sampling: SampleConstructor, EnvironmentOnly: true})
	registerBinding(ConsumerBinding{ID: "instrumentation.analytics.DD_TRACE_SARAMA_ANALYTICS_ENABLED", Consumer: "instrumentation.AnalyticsRate", Keys: []string{"DD_TRACE_SARAMA_ANALYTICS_ENABLED"}, Sampling: SampleConstructor, EnvironmentOnly: true})
	registerBinding(ConsumerBinding{ID: "instrumentation.analytics.DD_TRACE_SQL_ANALYTICS_ENABLED", Consumer: "instrumentation.AnalyticsRate", Keys: []string{"DD_TRACE_SQL_ANALYTICS_ENABLED"}, Sampling: SampleConstructor, EnvironmentOnly: true})
	registerBinding(ConsumerBinding{ID: "instrumentation.analytics.DD_TRACE_TWIRP_ANALYTICS_ENABLED", Consumer: "instrumentation.AnalyticsRate", Keys: []string{"DD_TRACE_TWIRP_ANALYTICS_ENABLED"}, Sampling: SampleConstructor, EnvironmentOnly: true})
	registerBinding(ConsumerBinding{ID: "instrumentation.analytics.DD_TRACE_VALKEY_ANALYTICS_ENABLED", Consumer: "instrumentation.AnalyticsRate", Keys: []string{"DD_TRACE_VALKEY_ANALYTICS_ENABLED"}, Sampling: SampleConstructor, EnvironmentOnly: true})
	registerBinding(ConsumerBinding{ID: "instrumentation.analytics.DD_TRACE_VAULT_ANALYTICS_ENABLED", Consumer: "instrumentation.AnalyticsRate", Keys: []string{"DD_TRACE_VAULT_ANALYTICS_ENABLED"}, Sampling: SampleConstructor, EnvironmentOnly: true})
	registerBinding(ConsumerBinding{ID: "instrumentation.analytics.DD_TRACE_ZAP_ANALYTICS_ENABLED", Consumer: "instrumentation.AnalyticsRate", Keys: []string{"DD_TRACE_ZAP_ANALYTICS_ENABLED"}, Sampling: SampleConstructor, EnvironmentOnly: true})
	registerBinding(ConsumerBinding{ID: "instrumentation.analytics.DD_TRACE_ZEROLOG_ANALYTICS_ENABLED", Consumer: "instrumentation.AnalyticsRate", Keys: []string{"DD_TRACE_ZEROLOG_ANALYTICS_ENABLED"}, Sampling: SampleConstructor, EnvironmentOnly: true})
	integrationAnalyticsBindings = make(map[string]ConsumerBinding, len(integrationAnalyticsKeys))
	for _, key := range integrationAnalyticsKeys {
		binding := ConsumerBinding{
			ID: "instrumentation.analytics." + key, Consumer: "instrumentation.AnalyticsRate",
			Keys: []string{key}, Sampling: SampleConstructor, EnvironmentOnly: true,
		}
		integrationAnalyticsBindings[key] = binding
	}
}

func resolveBound[T any](def RawDefinition, binding ConsumerBinding, defaultValue T, parse schema.Parser[T]) (schema.Resolved[T], []ConfigEvent) {
	return resolveBoundWithProvider(providerFor(def, binding), def, binding, defaultValue, parse)
}

func resolveBoundWithProvider[T any](p *provider.Provider, def RawDefinition, binding ConsumerBinding, defaultValue T, parse schema.Parser[T]) (schema.Resolved[T], []ConfigEvent) {
	return provider.ResolveWithBinding(p, def, binding, defaultValue, parse)
}

func providerFor(def RawDefinition, binding ConsumerBinding) *provider.Provider {
	if binding.EnvironmentOnly || def.Sources == SourceEnvironment {
		return newEnvironmentProvider()
	}
	return newStableProvider()
}

func registeredDefinition(key string) RawDefinition {
	instrumentationRawIndex.once.Do(func() {
		raw, _ := RegisteredDefinitions()
		index := make(map[string]RawDefinition, len(raw))
		for _, def := range raw {
			index[def.Key] = def
		}
		instrumentationRawIndex.byKey = index
	})
	if def, ok := instrumentationRawIndex.byKey[key]; ok {
		return def
	}
	panic("config definition not registered: " + key)
}

func resolveString(key string, binding ConsumerBinding) (schema.Resolved[string], []ConfigEvent) {
	def := registeredDefinition(key)
	return resolveStringWithProvider(providerFor(def, binding), def, binding)
}

func resolveStringWithProvider(p *provider.Provider, def RawDefinition, binding ConsumerBinding) (schema.Resolved[string], []ConfigEvent) {
	return resolveBoundWithProvider(p, def, binding, "", func(raw string) (string, error) {
		return raw, nil
	})
}

func resolveNonEmptyString(key string, binding ConsumerBinding) (schema.Resolved[string], []ConfigEvent) {
	def := registeredDefinition(key)
	return resolveBoundWithProvider(providerFor(def, binding), def, binding, "", func(raw string) (string, error) {
		if raw == "" {
			return "", errors.New("empty value")
		}
		return raw, nil
	})
}

func resolveBool(key string, binding ConsumerBinding, defaultValue bool) (schema.Resolved[bool], []ConfigEvent) {
	def := registeredDefinition(key)
	return resolveBoolWithProvider(providerFor(def, binding), def, binding, defaultValue)
}

func resolveBoolWithProvider(p *provider.Provider, def RawDefinition, binding ConsumerBinding, defaultValue bool) (schema.Resolved[bool], []ConfigEvent) {
	resolved, events := resolveBoolQuietWithProvider(p, def, binding, defaultValue)
	for _, attempt := range resolved.Attempts {
		if attempt.Present && attempt.Err != nil && attempt.Origin == telemetry.OriginEnvVar {
			log.Warn("Non-boolean value for env var %s. Parse failed with error: %v", def.Key, attempt.Err.Error())
		}
	}
	return resolved, events
}

func resolveBoolQuiet(key string, binding ConsumerBinding, defaultValue bool) (schema.Resolved[bool], []ConfigEvent) {
	def := registeredDefinition(key)
	return resolveBoolQuietWithProvider(providerFor(def, binding), def, binding, defaultValue)
}

func resolveBoolQuietWithProvider(p *provider.Provider, def RawDefinition, binding ConsumerBinding, defaultValue bool) (schema.Resolved[bool], []ConfigEvent) {
	return resolveBoundWithProvider(p, def, binding, defaultValue, strconv.ParseBool)
}

func reportInstrumentationEvents(events []ConfigEvent) {
	instrumentationReporter.Report(events, instrumentationGeneration.Add(1))
}

func reportTracerCandidateEvents(candidate *Config, events []ConfigEvent) {
	if candidate == nil {
		panic("config: tracer candidate is nil")
	}
	candidate.StagePublicationConfigEvents(events)
}

func sourcePresent(attempts []schema.SourceAttempt) bool {
	for _, attempt := range attempts {
		if attempt.Present {
			return true
		}
	}
	return false
}

func sourceValid(attempts []schema.SourceAttempt) bool {
	for _, attempt := range attempts {
		if attempt.Present && attempt.Valid {
			return true
		}
	}
	return false
}

// HTTPTraceConfig is the constructor-scoped environment configuration used by
// instrumentation/httptrace.
type HTTPTraceConfig struct {
	QueryStringDisabled                      bool
	QueryStringRegexp                        string
	QueryStringRegexpPresent                 bool
	TraceClientIPEnabled                     bool
	ServerErrorStatuses                      string
	InferredProxyServicesEnabled             bool
	PubsubPropagationAsSpanLinks             bool
	BaggageTagKeys                           string
	BaggageTagKeysPresent                    bool
	ResourceRenamingEnabled                  bool
	ResourceRenamingEnabledPresent           bool
	ResourceRenamingAlwaysSimplifiedEndpoint bool
	QueryStringAllowlist                     string
	QueryStringAllowlistPresent              bool
	ClientQueryStringAllowlist               string
	ClientQueryStringAllowlistPresent        bool
	ServerQueryStringAllowlist               string
	ServerQueryStringAllowlistPresent        bool
}

// HTTPTraceSnapshot resolves one complete HTTP instrumentation configuration.
func HTTPTraceSnapshot() HTTPTraceConfig {
	var events []ConfigEvent
	p := newEnvironmentProvider()
	resolveStringField := func(key string) schema.Resolved[string] {
		def := registeredDefinition(key)
		resolved, local := resolveStringWithProvider(p, def, httpTraceBinding)
		events = append(events, local...)
		return resolved
	}
	resolveBoolField := func(key string, defaultValue bool) schema.Resolved[bool] {
		def := registeredDefinition(key)
		resolved, local := resolveBoolWithProvider(p, def, httpTraceBinding, defaultValue)
		events = append(events, local...)
		return resolved
	}

	queryDisabled := resolveBoolField("DD_TRACE_HTTP_URL_QUERY_STRING_DISABLED", false)
	queryRegexp := resolveStringField("DD_TRACE_OBFUSCATION_QUERY_STRING_REGEXP")
	clientIP := resolveBoolField("DD_TRACE_CLIENT_IP_ENABLED", false)
	errorStatuses := resolveStringField("DD_TRACE_HTTP_SERVER_ERROR_STATUSES")
	inferredProxy := resolveBoolField("DD_TRACE_INFERRED_PROXY_SERVICES_ENABLED", false)
	pubsubLinks := resolveBoolField("DD_GOOGLE_CLOUD_PUBSUB_PROPAGATION_AS_SPAN_LINKS", false)
	baggageKeys := resolveStringField("DD_TRACE_BAGGAGE_TAG_KEYS")
	resourceRenaming := resolveBoolField("DD_TRACE_RESOURCE_RENAMING_ENABLED", false)
	resourceRenamingAlways := resolveBoolField("DD_TRACE_RESOURCE_RENAMING_ALWAYS_SIMPLIFIED_ENDPOINT", false)
	allowlist := resolveStringField("DD_TRACE_HTTP_URL_QUERY_STRING_ALLOWLIST")
	clientAllowlist := resolveStringField("DD_TRACE_HTTP_URL_QUERY_STRING_ALLOWLIST_CLIENT")
	serverAllowlist := resolveStringField("DD_TRACE_HTTP_URL_QUERY_STRING_ALLOWLIST_SERVER")
	reportInstrumentationEvents(events)

	return HTTPTraceConfig{
		QueryStringDisabled:                      queryDisabled.Winner.Value,
		QueryStringRegexp:                        queryRegexp.Winner.Value,
		QueryStringRegexpPresent:                 sourcePresent(queryRegexp.Attempts),
		TraceClientIPEnabled:                     clientIP.Winner.Value,
		ServerErrorStatuses:                      errorStatuses.Winner.Value,
		InferredProxyServicesEnabled:             inferredProxy.Winner.Value,
		PubsubPropagationAsSpanLinks:             pubsubLinks.Winner.Value,
		BaggageTagKeys:                           baggageKeys.Winner.Value,
		BaggageTagKeysPresent:                    sourcePresent(baggageKeys.Attempts),
		ResourceRenamingEnabled:                  resourceRenaming.Winner.Value,
		ResourceRenamingEnabledPresent:           sourceValid(resourceRenaming.Attempts),
		ResourceRenamingAlwaysSimplifiedEndpoint: resourceRenamingAlways.Winner.Value,
		QueryStringAllowlist:                     allowlist.Winner.Value,
		QueryStringAllowlistPresent:              sourcePresent(allowlist.Attempts),
		ClientQueryStringAllowlist:               clientAllowlist.Winner.Value,
		ClientQueryStringAllowlistPresent:        sourcePresent(clientAllowlist.Attempts),
		ServerQueryStringAllowlist:               serverAllowlist.Winner.Value,
		ServerQueryStringAllowlistPresent:        sourcePresent(serverAllowlist.Attempts),
	}
}

// PropagationConfig is the environment view sampled by each public propagator
// construction.
type PropagationConfig struct {
	ExtractFirst    bool
	BehaviorExtract string
	InjectStyle     string
	ExtractStyle    string
	Style           string
}

// PropagationRequest describes programmatic propagation values. Empty fields
// are resolved from configuration; populated fields are preserved without
// reading their corresponding configuration keys.
type PropagationRequest struct {
	ExtractFirst    *bool
	BehaviorExtract string
	InjectStyle     string
	ExtractStyle    string
	ResolveStyles   bool
}

// NewPropagationSnapshot resolves only public propagation settings.
func NewPropagationSnapshot() PropagationConfig {
	return NewPropagationSnapshotFor(PropagationRequest{ResolveStyles: true})
}

// NewPropagationSnapshotFor resolves only propagation settings not supplied
// programmatically. All configuration events are reported together after the
// requested fields have been resolved.
func NewPropagationSnapshotFor(request PropagationRequest) PropagationConfig {
	var events []ConfigEvent
	p := newEnvironmentProvider()

	snapshot := PropagationConfig{
		BehaviorExtract: request.BehaviorExtract,
		InjectStyle:     request.InjectStyle,
		ExtractStyle:    request.ExtractStyle,
	}
	if request.ExtractFirst != nil {
		snapshot.ExtractFirst = *request.ExtractFirst
	} else {
		def := registeredDefinition("DD_TRACE_PROPAGATION_EXTRACT_FIRST")
		resolved, local := resolveBoolWithProvider(p, def, propagationBinding, false)
		events = append(events, local...)
		snapshot.ExtractFirst = resolved.Winner.Value
	}
	if request.BehaviorExtract == "" {
		def := registeredDefinition("DD_TRACE_PROPAGATION_BEHAVIOR_EXTRACT")
		resolved, local := resolveStringWithProvider(p, def, propagationBinding)
		events = append(events, local...)
		snapshot.BehaviorExtract = resolved.Winner.Value
	}

	if !request.ResolveStyles {
		reportInstrumentationEvents(events)
		return snapshot
	}

	if request.InjectStyle == "" {
		def := registeredDefinition("DD_TRACE_PROPAGATION_STYLE_INJECT")
		resolved, local := resolveStringWithProvider(p, def, propagationBinding)
		events = append(events, local...)
		snapshot.InjectStyle = resolved.Winner.Value
	}
	if request.ExtractStyle == "" {
		def := registeredDefinition("DD_TRACE_PROPAGATION_STYLE_EXTRACT")
		resolved, local := resolveStringWithProvider(p, def, propagationBinding)
		events = append(events, local...)
		snapshot.ExtractStyle = resolved.Winner.Value
	}
	if (request.InjectStyle == "" && snapshot.InjectStyle == "") ||
		(request.ExtractStyle == "" && snapshot.ExtractStyle == "") {
		def := registeredDefinition("DD_TRACE_PROPAGATION_STYLE")
		resolved, local := resolveBoundWithProvider(p, def, propagationBinding, "", func(raw string) (string, error) {
			if raw == "" {
				return "", errors.New("empty value")
			}
			return raw, nil
		})
		events = append(events, local...)
		snapshot.Style = resolved.Winner.Value
	}
	reportInstrumentationEvents(events)
	return snapshot
}

// QueryStringRegexp resolves only the HTTP query-string obfuscation regexp.
func QueryStringRegexp() (value string, present bool) {
	resolved, events := resolveString("DD_TRACE_OBFUSCATION_QUERY_STRING_REGEXP", httpTraceBinding)
	reportInstrumentationEvents(events)
	return resolved.Winner.Value, sourcePresent(resolved.Attempts)
}

// StoppedTracerTag identifies one stopped-tracer environment fallback.
type StoppedTracerTag uint8

const (
	StoppedTracerEnvironment StoppedTracerTag = iota
	StoppedTracerVersion
)

// StoppedTracerTagValue resolves one environment-only fallback tag after the
// tracer has stopped.
func StoppedTracerTagValue(tag StoppedTracerTag) string {
	key := "DD_ENV"
	if tag == StoppedTracerVersion {
		key = "DD_VERSION"
	}
	binding := ConsumerBinding{
		ID: "tracer.stopped-tags", Consumer: "ddtrace/tracer.Span.Format",
		Keys: []string{"DD_ENV", "DD_VERSION"}, Sampling: SamplePerCall,
		EnvironmentOnly: true,
	}
	resolved, events := resolveString(key, binding)
	reportInstrumentationEvents(events)
	return resolved.Winner.Value
}

// TraceIDLoggingEnabled resolves the per-format-call trace ID logging mode.
func TraceIDLoggingEnabled() bool {
	binding := ConsumerBinding{
		ID: "tracer.trace-id-logging", Consumer: "ddtrace/tracer.Span.Format",
		Keys: []string{"DD_TRACE_128_BIT_TRACEID_LOGGING_ENABLED"}, Sampling: SamplePerCall,
		EnvironmentOnly: true,
	}
	resolved, events := resolveBool("DD_TRACE_128_BIT_TRACEID_LOGGING_ENABLED", binding, true)
	reportInstrumentationEvents(events)
	return resolved.Winner.Value
}

// TraceID128BitGenerationEnabledAtInit returns the environment-only package
// initialization fallback used before a tracer generation exists.
func TraceID128BitGenerationEnabledAtInit() bool {
	binding := ConsumerBinding{
		ID: "tracer.trace-id-generation-init", Consumer: "ddtrace/tracer package init",
		Keys: []string{"DD_TRACE_128_BIT_TRACEID_GENERATION_ENABLED"}, Sampling: SamplePackageInit,
		EnvironmentOnly: true,
	}
	resolved, events := resolveBool("DD_TRACE_128_BIT_TRACEID_GENERATION_ENABLED", binding, true)
	reportInstrumentationEvents(events)
	return resolved.Winner.Value
}

// SeelogWorkaroundEnabledAtInit preserves the legacy exact "false" opt-out.
func SeelogWorkaroundEnabledAtInit() bool {
	binding := ConsumerBinding{
		ID: "tracer.seelog-init", Consumer: "ddtrace/tracer package init",
		Keys: []string{"DD_TRACE_DEBUG_SEELOG_WORKAROUND"}, Sampling: SamplePackageInit,
		EnvironmentOnly: true,
	}
	resolved, events := resolveString("DD_TRACE_DEBUG_SEELOG_WORKAROUND", binding)
	reportInstrumentationEvents(events)
	return resolved.Winner.Value != "false"
}

// TracerLLMObsConfig is sampled once per tracer construction.
type TracerLLMObsConfig struct {
	Enabled          bool
	MLApp            string
	AgentlessEnabled *bool
	ProjectName      string
}

// TracerLLMObsSnapshot resolves the LLM Observability environment settings and
// stages their telemetry on candidate until that generation is published.
func TracerLLMObsSnapshot(candidate *Config) TracerLLMObsConfig {
	binding := ConsumerBinding{
		ID: "tracer.llmobs", Consumer: "ddtrace/tracer",
		Keys:     []string{"DD_LLMOBS_ENABLED", "DD_LLMOBS_ML_APP", "DD_LLMOBS_AGENTLESS_ENABLED", "DD_LLMOBS_PROJECT_NAME"},
		Sampling: SampleTracerConstruction, EnvironmentOnly: true,
	}
	var events []ConfigEvent
	p := newEnvironmentProvider()
	enabled, local := resolveBoolWithProvider(p, registeredDefinition("DD_LLMOBS_ENABLED"), binding, false)
	events = append(events, local...)
	mlApp, local := resolveStringWithProvider(p, registeredDefinition("DD_LLMOBS_ML_APP"), binding)
	events = append(events, local...)
	agentless, local := resolveBoolWithProvider(p, registeredDefinition("DD_LLMOBS_AGENTLESS_ENABLED"), binding, false)
	events = append(events, local...)
	projectName, local := resolveStringWithProvider(p, registeredDefinition("DD_LLMOBS_PROJECT_NAME"), binding)
	events = append(events, local...)
	reportTracerCandidateEvents(candidate, events)

	var agentlessValue *bool
	if sourceValid(agentless.Attempts) {
		value := agentless.Winner.Value
		agentlessValue = &value
	}
	return TracerLLMObsConfig{
		Enabled:          enabled.Winner.Value,
		MLApp:            mlApp.Winner.Value,
		AgentlessEnabled: agentlessValue,
		ProjectName:      projectName.Winner.Value,
	}
}

// APMTracingEnabled resolves stable APM tracing enablement and stages its
// telemetry on candidate until that generation is published.
func APMTracingEnabled(candidate *Config) bool {
	binding := ConsumerBinding{
		ID: "tracer.apm-tracing", Consumer: "ddtrace/tracer",
		Keys: []string{"DD_APM_TRACING_ENABLED"}, Sampling: SampleTracerConstruction,
	}
	resolved, events := resolveBoolQuiet("DD_APM_TRACING_ENABLED", binding, true)
	reportTracerCandidateEvents(candidate, events)
	return resolved.Winner.Value
}

// GraphQLErrorExtensions returns the raw constructor-scoped extension list.
func GraphQLErrorExtensions() string {
	binding := ConsumerBinding{
		ID: "instrumentation.graphql-error-extensions", Consumer: "instrumentation/graphql",
		Keys: []string{"DD_TRACE_GRAPHQL_ERROR_EXTENSIONS"}, Sampling: SampleConstructor,
		EnvironmentOnly: true,
	}
	resolved, events := resolveString("DD_TRACE_GRAPHQL_ERROR_EXTENSIONS", binding)
	reportInstrumentationEvents(events)
	return resolved.Winner.Value
}

// NamingSchemaConfig is the environment-only naming view.
type NamingSchemaConfig struct {
	Schema                        string
	RemoveIntegrationServiceNames bool
}

func namingSchemaSnapshot(binding ConsumerBinding, candidate *Config) NamingSchemaConfig {
	p := newEnvironmentProvider()
	schemaValue, schemaEvents := resolveStringWithProvider(p, registeredDefinition("DD_TRACE_SPAN_ATTRIBUTE_SCHEMA"), binding)
	removeNames, removeEvents := resolveBoolWithProvider(p, registeredDefinition("DD_TRACE_REMOVE_INTEGRATION_SERVICE_NAMES_ENABLED"), binding, false)
	events := append(schemaEvents, removeEvents...)
	if candidate == nil {
		reportInstrumentationEvents(events)
	} else {
		reportTracerCandidateEvents(candidate, events)
	}
	return NamingSchemaConfig{
		Schema:                        schemaValue.Winner.Value,
		RemoveIntegrationServiceNames: removeNames.Winner.Value,
	}
}

// InstrumentationNamingSchemaSnapshot resolves the package-init naming view.
func InstrumentationNamingSchemaSnapshot() NamingSchemaConfig {
	return namingSchemaSnapshot(ConsumerBinding{
		ID: "instrumentation.naming-init", Consumer: "instrumentation/internal/namingschema",
		Keys:     []string{"DD_TRACE_SPAN_ATTRIBUTE_SCHEMA", "DD_TRACE_REMOVE_INTEGRATION_SERVICE_NAMES_ENABLED"},
		Sampling: SamplePackageInit, EnvironmentOnly: true,
	}, nil)
}

// TracerNamingSchemaSnapshot resolves naming for one tracer construction and
// stages its telemetry on candidate until that generation is published.
func TracerNamingSchemaSnapshot(candidate *Config) NamingSchemaConfig {
	if candidate == nil {
		panic("config: tracer candidate is nil")
	}
	return namingSchemaSnapshot(ConsumerBinding{
		ID: "tracer.naming", Consumer: "internal/namingschema",
		Keys:     []string{"DD_TRACE_SPAN_ATTRIBUTE_SCHEMA", "DD_TRACE_REMOVE_INTEGRATION_SERVICE_NAMES_ENABLED"},
		Sampling: SampleTracerConstruction, EnvironmentOnly: true,
	}, candidate)
}

// ProcessNamingSchemaSnapshot resolves the immediate naming view used outside
// tracer generation construction.
func ProcessNamingSchemaSnapshot() NamingSchemaConfig {
	return namingSchemaSnapshot(ConsumerBinding{
		ID: "naming.process-init", Consumer: "internal/namingschema process initialization",
		Keys:     []string{"DD_TRACE_SPAN_ATTRIBUTE_SCHEMA", "DD_TRACE_REMOVE_INTEGRATION_SERVICE_NAMES_ENABLED"},
		Sampling: SamplePackageInit, EnvironmentOnly: true,
	}, nil)
}

// NamingServiceName returns the environment-only service used by test reloads.
func NamingServiceName() string {
	binding := ConsumerBinding{
		ID: "naming.service-reload", Consumer: "naming schema test reload",
		Keys: []string{"DD_SERVICE"}, Sampling: SampleConstructor, EnvironmentOnly: true,
	}
	resolved, events := resolveString("DD_SERVICE", binding)
	reportInstrumentationEvents(events)
	return resolved.Winner.Value
}

// IntegrationAnalyticsEnabled resolves an integration analytics flag.
//
// Built-in integrations use registered definitions and bounded telemetry state.
// External integrations may supply arbitrary prefixes through PackageInfo; the
// fallback remains environment-only and intentionally emits no value telemetry
// or prefix-keyed reporter state.
func IntegrationAnalyticsEnabled(envVarPrefix string) bool {
	key := "DD_TRACE_" + envVarPrefix + "_ANALYTICS_ENABLED"
	binding, ok := integrationAnalyticsBindings[key]
	if !ok {
		binding = ConsumerBinding{
			ID: "instrumentation.analytics.external", Consumer: "instrumentation.AnalyticsRate",
			Keys: []string{key}, Sampling: SampleConstructor, EnvironmentOnly: true,
		}
		resolved, _ := resolveBound(
			RawDefinition{Key: key, Sources: SourceEnvironment, Telemetry: TelemetryOmit},
			binding,
			false,
			strconv.ParseBool,
		)
		for _, attempt := range resolved.Attempts {
			if attempt.Present && attempt.Err != nil && attempt.Origin == telemetry.OriginEnvVar {
				log.Warn("Non-boolean value for env var %s. Parse failed with error: %v", key, attempt.Err.Error())
			}
		}
		return resolved.Winner.Value
	}
	resolved, events := resolveBool(key, binding, false)
	reportInstrumentationEvents(events)
	return resolved.Winner.Value
}

// IntegrationAnalyticsRegistered reports whether envVarPrefix uses a fixed,
// bounded registry binding rather than the external-integration fallback.
func IntegrationAnalyticsRegistered(envVarPrefix string) bool {
	_, ok := integrationAnalyticsBindings["DD_TRACE_"+envVarPrefix+"_ANALYTICS_ENABLED"]
	return ok
}

// APISecurityEndpointCollectionEnabled resolves the constructor-scoped API
// Security endpoint collection flag.
func APISecurityEndpointCollectionEnabled() bool {
	binding := ConsumerBinding{
		ID: "instrumentation.api-security-endpoint-collection", Consumer: "instrumentation",
		Keys: []string{"DD_API_SECURITY_ENDPOINT_COLLECTION_ENABLED"}, Sampling: SampleConstructor,
		EnvironmentOnly: true,
	}
	resolved, events := resolveBool("DD_API_SECURITY_ENDPOINT_COLLECTION_ENABLED", binding, true)
	reportInstrumentationEvents(events)
	return resolved.Winner.Value
}

// InstrumentationDataStreamsEnabled resolves stable Data Streams enablement
// for an instrumentation constructor.
func InstrumentationDataStreamsEnabled() bool {
	binding := ConsumerBinding{
		ID: "instrumentation.data-streams", Consumer: "instrumentation",
		Keys: []string{"DD_DATA_STREAMS_ENABLED"}, Sampling: SampleConstructor,
	}
	resolved, events := resolveBoolQuiet("DD_DATA_STREAMS_ENABLED", binding, false)
	reportInstrumentationEvents(events)
	return resolved.Winner.Value
}

// PubsubPropagationAsSpanLinks resolves the Pub/Sub constructor setting.
func PubsubPropagationAsSpanLinks() bool {
	binding := ConsumerBinding{
		ID: "instrumentation.pubsub-span-links", Consumer: "contrib/cloud.google.com/go/pubsubtrace",
		Keys: []string{"DD_GOOGLE_CLOUD_PUBSUB_PROPAGATION_AS_SPAN_LINKS"}, Sampling: SampleConstructor,
		EnvironmentOnly: true,
	}
	resolved, events := resolveBoolQuiet("DD_GOOGLE_CLOUD_PUBSUB_PROPAGATION_AS_SPAN_LINKS", binding, false)
	reportInstrumentationEvents(events)
	return resolved.Winner.Value
}

// TracerOTelDDValue preserves the legacy tracer OTel-to-Datadog compatibility
// lookup without leaving raw reads in the tracer package.
func TracerOTelDDValue(configName string) string {
	binding, ok := tracerOTelBindings[configName]
	if !ok {
		log.Debug("Programming Error: %s not found in supported configurations", configName)
		return ""
	}
	key := tracerOTelDDKeys[configName]
	resolved, events := provider.ResolveTracerOTelCompatibility(
		newStableProvider(),
		registeredDefinition(key),
		binding,
	)
	reportInstrumentationEvents(events)
	return resolved.Winner.Value
}
