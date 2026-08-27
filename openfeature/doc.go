// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

// Package openfeature provides an OpenFeature-compatible feature flag provider
// for server-side feature flag evaluation, backed by configuration delivered
// from Datadog.
//
// # Overview
//
// This package implements the OpenFeature Provider interface, allowing applications
// to evaluate feature flags using configurations delivered dynamically from Datadog.
// Configuration reaches the provider one of two ways: Agentless, polling a Datadog
// endpoint directly over HTTPS (the default), or through the Agent's Remote Config.
// Both feed the same evaluator; only delivery differs. See "# Configuration Source"
// below. The provider supports all standard OpenFeature flag types: boolean, string,
// integer, float, and JSON objects.
//
// # Key Features
//
//   - Dynamic flag configuration via Agentless polling or Datadog Remote Config
//   - Support for all OpenFeature flag types (boolean, string, integer, float, JSON)
//   - Advanced targeting with attribute-based conditions
//   - Traffic sharding for gradual rollouts and A/B testing
//   - Time-based flag scheduling (start/end times)
//   - Thread-safe concurrent flag evaluation
//   - Comprehensive error handling with proper OpenFeature error codes
//
// # Basic Usage
//
// To use the Datadog OpenFeature provider, create a new provider instance and
// register it with the OpenFeature SDK:
//
//		import (
//		    ddopenfeature "github.com/DataDog/dd-trace-go/v2/openfeature"
//		    of "github.com/open-feature/go-sdk/openfeature"
//		)
//
//		// Create and register the provider
//		provider, err := ddopenfeature.NewDatadogProvider(ddopenfeature.ProviderConfig{})
//		if err != nil {
//		    log.Fatal(err)
//		}
//		defer provider.Shutdown()
//
//	 // This can take up to 30 seconds (the default provider initialization timeout) as it waits for initialization
//		err = of.SetProviderAndWait(provider)
//		if err != nil {
//		    log.Fatal(err)
//		}
//
//		// Create a client and evaluate flags
//		client := of.NewClient("my-app")
//		ctx := context.Background()
//
//		// Evaluate a boolean flag with a targetless context
//		evalCtx := of.NewTargetlessEvaluationContext()
//		enabled, err := client.BooleanValue(ctx, "new-feature", false, evalCtx)
//		if err != nil {
//		    log.Printf("Failed to evaluate flag: %v", err)
//		}
//
//		if enabled {
//		    // Execute new feature code
//		}
//
// # Targeting Context
//
// The provider supports attribute-based targeting using the OpenFeature evaluation
// context. You can pass user attributes to determine which flag variant a user receives:
//
//	evalCtx := of.NewEvaluationContext("user-123", map[string]interface{}{
//	    "country": "US",
//	    "tier": "premium",
//	    "age": 25,
//	    "email": "user@example.com",
//	})
//
//	value, err := client.StringValue(ctx, "api-version", "v1", evalCtx)
//
// The provider automatically looks for a targeting key "targetingKey" (OpenFeature standard)
//
// # Flag Configuration
//
// Feature flags are configured through Datadog Remote Config and include:
//
//   - Flag metadata (key, enabled status, variation type)
//   - Variants with their values
//   - Allocation rules with targeting conditions
//   - Traffic distribution (sharding) configuration
//   - Optional time windows for scheduled rollouts
//
// The configuration format follows the Datadog Feature Flag Evaluation (FFE) schema.
//
// # Evaluation Logic
//
// Flag evaluation follows this order:
//
//  1. Check if configuration is loaded (error if not)
//  2. Check if flag exists (FLAG_NOT_FOUND error if not)
//  3. Check if flag is enabled (return default with DISABLED reason if not)
//  4. Evaluate allocations in order (first match wins):
//     a. Check time window constraints (startAt/endAt)
//     b. Evaluate targeting rules (OR logic between rules, AND within conditions)
//     c. Evaluate traffic sharding (consistent hash-based distribution)
//  5. Return matched variant or default value
//
// # Targeting Conditions
//
// The provider supports various condition operators:
//
// Numeric comparisons:
//   - LT, LTE, GT, GTE: Compare numeric attributes
//
// Semantic version comparisons:
//   - SEMVER_EQ, SEMVER_NEQ, SEMVER_LT, SEMVER_LTE, SEMVER_GT, SEMVER_GTE: Compare semantic version strings
//
// String matching:
//   - MATCHES, NOT_MATCHES: Regex pattern matching
//
// Set membership:
//   - ONE_OF, NOT_ONE_OF: Check if attribute is in a list
//
// Null checks:
//   - IS_NULL: Check if attribute is present or absent
//
// Example configuration structure (conceptual):
//
//	{
//	  "flags": {
//	    "premium-feature": {
//	      "key": "premium-feature",
//	      "enabled": true,
//	      "variationType": "BOOLEAN",
//	      "variations": {
//	        "on": {"key": "on", "value": true},
//	        "off": {"key": "off", "value": false}
//	      },
//	      "allocations": [{
//	        "key": "premium-users",
//	        "rules": [{
//	          "conditions": [{
//	            "operator": "ONE_OF",
//	            "attribute": "tier",
//	            "value": ["premium", "enterprise"]
//	          }]
//	        }],
//	        "splits": [{
//	          "shards": [{
//	            "salt": "feature-salt",
//	            "ranges": [{"start": 0, "end": 8192}],
//	            "totalShards": 8192
//	          }],
//	          "variationKey": "on"
//	        }]
//	      }]
//	    }
//	  }
//	}
//
// # Traffic Sharding
//
// The provider uses consistent hashing (MD5) for deterministic traffic distribution.
// This ensures users consistently receive the same variant across evaluations.
//
// Sharding allows for:
//   - Gradual rollouts (e.g., 10% -> 50% -> 100%)
//   - A/B testing with precise traffic splits
//   - Canary deployments
//
// The default total shards is 8192, providing fine-grained control over traffic
// distribution percentages.
//
// # Error Handling
//
// The provider properly maps errors to OpenFeature error codes:
//
//   - FLAG_NOT_FOUND: Requested flag doesn't exist in configuration
//   - TYPE_MISMATCH: Flag value type doesn't match requested type
//   - PARSE_ERROR: Error parsing or converting flag value
//   - GENERAL: Other errors (e.g., no configuration loaded)
//
// Errors are returned through the OpenFeature ResolutionDetail, and the default
// value is returned when errors occur.
//
// # Thread Safety
//
// The provider is fully thread-safe and can handle concurrent flag evaluations
// while configuration updates are in progress. Configuration updates use a
// read-write mutex to ensure consistency.
//
// # Configuration Source
//
// DD_FEATURE_FLAGS_CONFIGURATION_SOURCE selects how configuration is delivered:
//
//   - "agentless" (default): the provider polls a Datadog endpoint directly over
//     HTTPS on an interval, without using the Agent for configuration. Requires
//     DD_API_KEY unless a custom endpoint is configured; DD_SITE optionally
//     selects the managed endpoint's host and has no effect without DD_API_KEY.
//     EVP event delivery may still use a compatible local Agent route. See
//     "# Environment Variables" below.
//   - "remote_config": the provider subscribes to Datadog Remote Config updates
//     using the FFE_FLAGS product (capability 46), via the Agent. Configuration
//     updates are acknowledged back to Remote Config with appropriate status
//     codes (acknowledged for success, error for validation failures).
//
// Both sources feed the same validation and evaluation logic; only how
// configuration arrives differs. Requesting configuration is billable. For the
// agentless source, nothing is sent to Datadog until NewDatadogProvider is
// called — creating the provider is the point at which billing begins. For
// the remote_config source, this guarantee does not hold if tracer.Start runs
// first: the tracer eagerly subscribes to the FFE_FLAGS Remote Config product
// and may fetch and buffer configuration before NewDatadogProvider is called.
//
// Exposure and flag-evaluation events use EVP. With the agentless configuration
// source, the first event flush queries the trace Agent's /info endpoint once,
// preferring /evp_proxy/v4 and then /evp_proxy/v2. When neither route is
// advertised or the Agent cannot be reached, delivery uses the direct
// event-platform intake derived from DD_SITE and authenticates with DD_API_KEY.
// Discovery runs only once. A selected local route can transition to direct
// delivery after an incompatible HTTP response or transport failure. Without a
// compatible Agent route or valid direct credentials, EVP event delivery is
// disabled. The remote_config source continues to send EVP events through the
// Agent's /evp_proxy/v2 route.
//
// # Configuration
//
// The provider can be configured using ProviderConfig when creating a new instance:
//
//	config := ddopenfeature.ProviderConfig{
//	    ExposureFlushInterval: 5 * time.Second,  // Optional: defaults to 1 second
//	}
//	provider, err := ddopenfeature.NewDatadogProvider(config)
//
// Configuration Options:
//
//   - ExposureFlushInterval: Duration between automatic flushes of exposure events
//     through EVP. Defaults to 1 second if not specified. Exposure events
//     track which feature flags are evaluated and by which users, providing visibility
//     into feature flag usage. Set to 0 to disable automatic flushing (not recommended).
//
// # Environment Variables
//
//   - DD_FEATURE_FLAGS_ENABLED: Stable kill switch, default "true". Set to "false"
//     to disable the provider entirely: NewDatadogProvider() returns a NoopProvider
//     instead of the actual Datadog provider, regardless of any other setting below.
//     Important: When using the NoopProvider, all flag evaluations will silently
//     return the default values you specify, with no errors. This allows your
//     application to run without feature flags being active. The NoopProvider
//     can also be combined with the OpenFeature multi-provider
//     (https://github.com/open-feature/go-sdk/tree/main/openfeature/multi)
//     to implement local overrides during development or testing.
//
//   - DD_FEATURE_FLAGS_CONFIGURATION_SOURCE: "agentless" (default), "remote_config",
//     or "offline" (explicitly disables configuration delivery). See
//     "# Configuration Source" above. An unrecognized non-blank value also disables
//     the provider (fails closed, to avoid silently starting billed polling on a typo).
//
//   - DD_FEATURE_FLAGS_CONFIGURATION_SOURCE_AGENTLESS_BASE_URL: Optional override of
//     the Agentless endpoint. Leave unset to use the managed, Datadog-hosted endpoint
//     derived from DD_SITE (requires DD_API_KEY). Set only to point at a custom
//     collector; a custom endpoint never receives DD_API_KEY.
//
//   - DD_FEATURE_FLAGS_CONFIGURATION_SOURCE_AGENTLESS_POLL_INTERVAL_SECONDS: Agentless
//     poll interval in seconds, default 30, valid range (0, 3600]. An out-of-range or
//     unparseable value falls back to the default rather than being clamped.
//
//   - DD_FEATURE_FLAGS_CONFIGURATION_SOURCE_AGENTLESS_REQUEST_TIMEOUT_SECONDS: Per-request
//     timeout in seconds for Agentless polls, default 5, valid range (0, 300]. An
//     out-of-range or unparseable value falls back to the default rather than being
//     clamped.
//
//   - DD_EXPERIMENTAL_FLAGGING_PROVIDER_ENABLED: Deprecated. Kept only to grandfather
//     existing adopters onto DD_FEATURE_FLAGS_CONFIGURATION_SOURCE=remote_config:
//     if DD_FEATURE_FLAGS_CONFIGURATION_SOURCE is not explicitly set and this legacy
//     variable is "true", the provider uses Remote Config as before. Prefer setting
//     DD_FEATURE_FLAGS_CONFIGURATION_SOURCE explicitly instead.
//
//   - DD_EXPERIMENTAL_FLAGGING_PROVIDER_SPAN_ENRICHMENT_ENABLED: When set to
//     "true", enables span enrichment — feature flag evaluation details are
//     recorded as tags on the active trace's root span. Default: false. Note:
//     the added span tags may affect APM billing.
//
//   - DD_EXPERIMENTAL_FLAGGING_PROVIDER_INITIALIZATION_TIMEOUT_MS: Timeout in
//     milliseconds for Init to wait for the first configuration before returning,
//     used only by Init (not InitWithContext, which takes its deadline from the
//     caller's context). Default 10000. An out-of-range (<= 0, or large enough to
//     overflow when converted to a time.Duration) or unparseable value falls back
//     to the default rather than being clamped.
//
//   - DD_FLAGGING_EVALUATION_COUNTS_ENABLED: Set to "false" to disable EVP
//     flag-evaluation count events. Defaults to true. Exposure events are not
//     affected.
//
// Example (Agentless, the default):
//
//	export DD_API_KEY=<your API key>
//	export DD_EXPERIMENTAL_FLAGGING_PROVIDER_SPAN_ENRICHMENT_ENABLED=true
//
// Standard Datadog environment variables also apply:
//
//   - DD_API_KEY: Required for the default, managed Agentless configuration
//     endpoint and direct EVP event fallback.
//   - DD_SITE: Datadog site (default: datadoghq.com). Determines the managed
//     Agentless configuration endpoint and direct EVP intake hosts.
//   - DD_TRACE_AGENT_URL: Trace Agent URL. Takes precedence over DD_AGENT_HOST
//     and DD_TRACE_AGENT_PORT. Used for remote_config and for Agentless EVP route
//     discovery and local event delivery.
//   - DD_AGENT_HOST: Trace Agent host (default: localhost). Used with
//     DD_TRACE_AGENT_PORT when DD_TRACE_AGENT_URL is unset.
//   - DD_TRACE_AGENT_PORT: Trace Agent port (default: 8126). Used with
//     DD_AGENT_HOST when DD_TRACE_AGENT_URL is unset.
//   - DD_SERVICE: Service name for tagging
//   - DD_ENV: Environment name (e.g., production, staging)
//   - DD_VERSION: Application version
//
// With no Agent address variables set, EVP route discovery uses the standard
// trace Agent resolution: an existing /var/run/datadog/apm.socket, then
// localhost:8126.
//
// # Prerequisites
//
// Calling tracer.Start() before creating the provider is recommended but not
// required. It matters for two things: the DD_TAGS "env:" fallback (used by
// the Agentless endpoint's dd_env query parameter) is only applied during
// tracer.Start, and for the remote_config source, if the tracer has already
// subscribed to Remote Config, the provider attaches to that existing
// subscription instead of starting its own client. If tracer.Start has not
// run, the remote_config source falls back to starting its own default
// Remote Config client — this does not require the tracer and does not error,
// though it means the application runs its own Remote Config client separate
// from the tracer's, if the tracer is started later.
//
// # Exposure Events and Deduplication
//
// The provider automatically tracks exposure events when feature flags are evaluated.
// Exposure events record which flags are evaluated and for which subjects (users),
// providing visibility into feature flag usage for analytics and experimentation.
//
// To avoid sending duplicate exposure events for repeated evaluations, the provider
// implements an LRU (Least Recently Used) cache for deduplication:
//
//   - Cache key: combination of flag key and subject ID
//   - Cache value: allocation key and variant
//   - Capacity: 65536 entries (2^16, ~6.5MB max memory)
//
// Deduplication behavior:
//
//   - Same subject evaluating the same flag multiple times: 1 exposure (deduplicated)
//   - Different subjects evaluating the same flag: 1 exposure per subject
//   - Same subject with variant change (A→B→A): 3 exposures (each change tracked)
//   - Same subject with allocation change: new exposure generated
//
// The cache uses LRU eviction when capacity is reached, ensuring recently active
// flag/subject combinations remain cached while older entries are evicted.
//
// Exposure events are buffered and flushed periodically through EVP (default:
// every 1 second, configurable via ExposureFlushInterval).
//
// # Performance Considerations
//
//   - Regex patterns are compiled once and cached for reuse
//   - Read locks are used for flag evaluation (multiple concurrent reads)
//   - Write locks only during configuration updates
//   - MD5 hashing is used for sharding (fast, non-cryptographic)
//   - Exposure deduplication uses O(1) LRU cache operations
//
// # Example: Complete Integration
//
//	package main
//
//	import (
//	    "context"
//	    "log"
//
//	    "github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
//	    ddopenfeature "github.com/DataDog/dd-trace-go/v2/openfeature"
//	    of "github.com/open-feature/go-sdk/openfeature"
//	)
//
//	func main() {
//	    // Start Datadog tracer (required for Remote Config)
//	    tracer.Start()
//	    defer tracer.Stop()
//
//	    // Create OpenFeature provider
//	    provider, err := ddopenfeature.NewDatadogProvider(ddopenfeature.ProviderConfig{})
//	    if err != nil {
//	        log.Fatalf("Failed to create provider: %v", err)
//	    }
//	    defer provider.Shutdown()
//
//	    // Register provider with OpenFeature
//	    if err := of.SetProviderAndWait(provider); err != nil {
//	        log.Fatalf("Failed to set provider: %v", err)
//	    }
//
//	    // Create client for your application
//	    client := of.NewClient("my-service")
//
//	    // Evaluate flags with user context
//	    ctx := context.Background()
//	    evalCtx := of.NewEvaluationContext("user-123", map[string]interface{}{
//	        "country": "US",
//	        "tier": "premium",
//	    })
//
//	    // Boolean flag
//	    if enabled, _ := client.BooleanValue(ctx, "new-checkout", false, evalCtx); enabled {
//	        log.Println("New checkout experience enabled")
//	    }
//
//	    // String flag
//	    apiVersion, _ := client.StringValue(ctx, "api-version", "v1", evalCtx)
//	    log.Printf("Using API version: %s", apiVersion)
//
//	    // Integer flag
//	    rateLimit, _ := client.IntValue(ctx, "rate-limit", 100, evalCtx)
//	    log.Printf("Rate limit: %d requests/minute", rateLimit)
//
//	    // Float flag
//	    discountRate, _ := client.FloatValue(ctx, "discount-rate", 0.0, evalCtx)
//	    log.Printf("Discount rate: %.2f%%", discountRate*100)
//
//	    // JSON/Object flag
//	    config, _ := client.ObjectValue(ctx, "feature-config", nil, evalCtx)
//	    log.Printf("Feature config: %+v", config)
//	}
//
// # Testing
//
// For unit testing code that uses feature flags, use the OpenFeature SDK's
// InMemoryProvider to define specific flag values:
//
//	import (
//	    of "github.com/open-feature/go-sdk/openfeature"
//	    "github.com/open-feature/go-sdk/openfeature/memprovider"
//	)
//
//	func TestMyFeature(t *testing.T) {
//	    // Create an in-memory provider with test flag values
//	    provider := memprovider.NewInMemoryProvider(map[string]memprovider.InMemoryFlag{
//	        "my-feature": {
//	            Key:            "my-feature",
//	            State:          memprovider.Enabled,
//	            DefaultVariant: "on",
//	            Variants: map[string]any{
//	                "on":  true,
//	                "off": false,
//	            },
//	        },
//	        "api-version": {
//	            Key:            "api-version",
//	            State:          memprovider.Enabled,
//	            DefaultVariant: "v2",
//	            Variants: map[string]any{
//	                "v1": "v1",
//	                "v2": "v2",
//	            },
//	        },
//	    })
//
//	    of.SetProviderAndWait(provider)
//	    defer of.Shutdown()
//
//	    client := of.NewClient("test-app")
//	    ctx := context.Background()
//
//	    // This will return true (the "on" variant)
//	    enabled, _ := client.BooleanValue(ctx, "my-feature", false,
//	        of.NewEvaluationContext("test-user", nil))
//
//	    if !enabled {
//	        t.Error("expected feature to be enabled")
//	    }
//	}
//
// The InMemoryProvider also supports context-based evaluation using ContextEvaluator
// for more complex test scenarios where the returned value depends on user attributes.
//
// For integration testing with real Remote Config delivery, set
// DD_FEATURE_FLAGS_CONFIGURATION_SOURCE=remote_config and ensure the Datadog
// agent is running in your test environment.
//
// # Limitations
//
//   - Configuration updates replace the entire flag set (no incremental updates)
//   - Provider shutdown doesn't fully unsubscribe from Remote Config yet (remote_config source only)
//   - Multi-config tracking (multiple Remote Config paths) not yet supported
//
// # Additional Resources
//
//   - OpenFeature Specification: https://openfeature.dev/specification/
//   - Datadog Remote Config: https://docs.datadoghq.com/agent/remote_config/
//   - Datadog APM Go SDK: https://docs.datadoghq.com/tracing/setup_overview/setup/go/
package openfeature
