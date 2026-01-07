// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

// Package openfeature provides an OpenFeature-compatible feature flag provider
// that integrates with Datadog Remote Config for server-side feature flag evaluation.
//
// # Overview
//
// This package implements the OpenFeature Provider interface, allowing applications
// to evaluate feature flags using configurations delivered dynamically through
// Datadog's Remote Config system. The provider supports all standard OpenFeature
// flag types: boolean, string, integer, float, and JSON objects.
//
// # Key Features
//
//   - Dynamic flag configuration via Datadog Remote Config
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
//		    "github.com/DataDog/dd-trace-go/v2/openfeature"
//		    of "github.com/open-feature/go-sdk/openfeature"
//		)
//
//		// Create and register the provider
//		provider, err := openfeature.NewDatadogProvider(openfeature.ProviderConfig{})
//		if err != nil {
//		    log.Fatal(err)
//		}
//		defer provider.Shutdown()
//
//	 // This can take up to 30 seconds (default timeout) as it waits for Remote Config initialization
//		err = of.SetProviderAndWait(provider)
//		if err != nil {
//		    log.Fatal(err)
//		}
//
//		// Create a client and evaluate flags
//		client := of.NewClient("my-app")
//		ctx := context.Background()
//
//		// Evaluate a boolean flag with an empty context
//		evalCtx := of.NewEvaluationContext("", nil)
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
// # Remote Config Integration
//
// The provider automatically subscribes to Datadog Remote Config updates using
// the FFE_FLAGS product (capability 46). When new configurations are received,
// they are validated and applied atomically.
//
// Configuration updates are acknowledged back to Remote Config with appropriate
// status codes (acknowledged for success, error for validation failures).
//
// # Configuration
//
// The provider can be configured using ProviderConfig when creating a new instance:
//
//	config := openfeature.ProviderConfig{
//	    ExposureFlushInterval: 5 * time.Second,  // Optional: defaults to 1 second
//	}
//	provider, err := openfeature.NewDatadogProvider(config)
//
// Configuration Options:
//
//   - ExposureFlushInterval: Duration between automatic flushes of exposure events
//     to the Datadog agent. Defaults to 1 second if not specified. Exposure events
//     track which feature flags are evaluated and by which users, providing visibility
//     into feature flag usage. Set to 0 to disable automatic flushing (not recommended).
//
// # Environment Variables
//
// The provider requires the following environment variable to be set:
//
//   - DD_EXPERIMENTAL_FLAGGING_PROVIDER_ENABLED: Must be set to "true" to enable
//     the OpenFeature provider. This is a safety flag to ensure the feature is
//     intentionally activated. If not set or set to false, NewDatadogProvider()
//     will return a NoopProvider instead of the actual Datadog provider.
//     Important: When using the NoopProvider, all flag evaluations will silently
//     return the default values you specify, with no errors. This allows your
//     application to run without feature flags being active.
//
// Example:
//
//	export DD_EXPERIMENTAL_FLAGGING_PROVIDER_ENABLED=true
//
// Standard Datadog environment variables also apply:
//
//   - DD_AGENT_HOST: Datadog agent host (default: localhost)
//   - DD_TRACE_AGENT_PORT: Datadog agent port (default: 8126)
//   - DD_SERVICE: Service name for tagging
//   - DD_ENV: Environment name (e.g., production, staging)
//   - DD_VERSION: Application version
//
// # Prerequisites
//
// Before creating the provider, ensure that:
//   - DD_EXPERIMENTAL_FLAGGING_PROVIDER_ENABLED environment variable is set to "true"
//   - The Datadog tracer is started (tracer.Start()) OR
//   - Remote Config client is properly configured
//
// If the default Remote Config setup fails, the provider creation will return
// an error asking you to call tracer.Start first.
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
// Exposure events are buffered and flushed periodically to the Datadog Agent
// (default: every 1 second, configurable via ExposureFlushInterval).
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
//	    "github.com/DataDog/dd-trace-go/v2/openfeature"
//	    of "github.com/open-feature/go-sdk/openfeature"
//	)
//
//	func main() {
//	    // Start Datadog tracer (required for Remote Config)
//	    tracer.Start()
//	    defer tracer.Stop()
//
//	    // Create OpenFeature provider
//	    provider, err := openfeature.NewDatadogProvider(openfeature.ProviderConfig{})
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
// For integration testing with real Datadog Remote Config, set the
// DD_EXPERIMENTAL_FLAGGING_PROVIDER_ENABLED environment variable and ensure
// the Datadog agent is running in your test environment
//
// # Limitations
//
//   - Configuration updates replace the entire flag set (no incremental updates)
//   - Provider shutdown doesn't fully unsubscribe from Remote Config yet
//   - Multi-config tracking (multiple Remote Config paths) not yet supported
//
// # Additional Resources
//
//   - OpenFeature Specification: https://openfeature.dev/specification/
//   - Datadog Remote Config: https://docs.datadoghq.com/agent/remote_config/
//   - Datadog APM Go SDK: https://docs.datadoghq.com/tracing/setup_overview/setup/go/
package openfeature
