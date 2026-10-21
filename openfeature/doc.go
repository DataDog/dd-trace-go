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
//	import (
//	    ddopenfeature "github.com/DataDog/dd-trace-go/v2/openfeature"
//	    of "github.com/open-feature/go-sdk/openfeature"
//	)
//
//	// Create and register the provider
//	provider, err := ddopenfeature.NewDatadogProvider(ddopenfeature.ProviderConfig{})
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer provider.Shutdown()
//
//	// Blocks while waiting for the first configuration, bounded by
//	// DD_EXPERIMENTAL_FLAGGING_PROVIDER_INITIALIZATION_TIMEOUT_MS (default 10s).
//	// On expiry it returns a recoverable PROVIDER_NOT_READY error: delivery keeps
//	// running and a later configuration transitions the provider to ready.
//	if err = of.SetProviderAndWait(provider); err != nil {
//	    log.Printf("feature flags are not ready yet: %v", err)
//	}
//
//	// Create a client and evaluate flags
//	client := of.NewClient("my-app")
//	ctx := context.Background()
//
//	// Evaluate a boolean flag with a targetless context
//	evalCtx := of.NewTargetlessEvaluationContext()
//	enabled, err := client.BooleanValue(ctx, "new-feature", false, evalCtx)
//	if err != nil {
//	    log.Printf("Failed to evaluate flag: %v", err)
//	}
//
//	if enabled {
//	    // Execute new feature code
//	}
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
// # Evaluation Telemetry Opt-Out
//
// Applications can enable a per-evaluation telemetry opt-out parameter:
//
//	provider, err := ddopenfeature.NewDatadogProvider(ddopenfeature.ProviderConfig{
//	    EnableEvaluationTelemetryOptOutParameter: new(true),
//	})
//	// Handle err and register provider with OpenFeature before evaluating flags.
//	evalCtx := of.NewEvaluationContext("user-123", map[string]any{
//	    "dd_evaluation_telemetry_opt_out": true,
//	})
//	value, err := client.BooleanValue(ctx, "new-feature", false, evalCtx)
//
// Alternatively, set DD_FEATURE_FLAGS_EVALUATION_TELEMETRY_PER_SUBJECT_OPT_OUT_ENABLED=true
// before SDK initialization and leave EnableEvaluationTelemetryOptOutParameter nil.
// An explicit new(true) or new(false) option overrides the environment setting.
// Activation defaults to false. The provider copies the resolved value at construction.
// When enabled, only boolean true in the final merged context suppresses new
// evaluation events, evaluation metrics, and automatic flag span enrichment.
// Missing values, false, null, strings, and numbers preserve existing behavior.
// The provider reads the parameter on every evaluation. It does not remember a
// subject's decision. Supply the parameter before each evaluation, either in its
// evaluation context or in the transaction context passed to that evaluation.
// Do not change a global or shared client context for individual server requests.
// The attribute remains available to targeting rules and exposure reporting.
// Flag values, experiment exposures, exposure deduplication, application hooks,
// and previously queued telemetry are unchanged.
//
// # Customer Exposure Hooks
//
// Register a customer callback with the OpenFeature client:
//
//	client.AddHooks(ddopenfeature.NewExposureHook(func(e ddopenfeature.ExposureEvaluation) {
//	    if !e.ShouldSend() {
//	        return
//	    }
//	    // Copy the fields into your own background queue. Do not block here.
//	    allocation, _ := e.AllocationKey()
//	    serialID, hasSerialID := e.SplitSerialID()
//	    enqueueRow(e.Details.FlagKey, e.Context.TargetingKey(), e.Details.Variant,
//	        allocation, serialID, hasSerialID, e.Features())
//	}))
//
// enqueueRow is application code, not a Datadog API. The callback runs on the
// evaluation goroutine and must support concurrent calls. Callback panics do not
// affect flag results or Datadog exposure logging.
// Create the hook once and reuse it, including when passing it to of.WithHooks
// on individual evaluations. Each hook warns on its first callback panic and
// logs later panics at debug level with the flag key.
// Treat Details.Value as read-only. JSON maps and slices can share provider state.
// Copy these values before modification or background processing.
//
// IsExposure reports successful resolution of an allocation with doLog enabled.
// IsCacheHit identifies repeats. ShouldSend selects new or changed exposures.
// The cache compares allocation, variant, and optional serial ID for each flag
// and subject. Serial ID zero is distinct from an absent serial ID. All customer
// hooks receive the same decision; one hook cannot consume another's exposure.
// A cache hit does not confirm successful delivery. Customers own queueing,
// retries, and shutdown. Use IsExposure with customer-owned deduplication when
// independent delivery guarantees are required.
//
// Features returns scalar metadata from the selected split's HOOK destination.
// For example, a holdout can supply holdout.key and holdout.assignment_group.
// EXPOSURE and EVALUATION destinations add features to the corresponding Datadog
// events, not to Features(). Destination filtering applies to these events and
// Features(); raw Details.FlagMetadata can contain internal fields for other
// destinations. It is not a destination-isolation boundary.
// Features are configuration metadata, not subject attributes. Evaluation events
// retain them when subject-data collection is disabled. Different evaluation
// feature values use separate aggregation buckets.
// Go retains at most 256 unique features per split. Keys and string values must
// fit within 256 UTF-8 bytes. Invalid entries are skipped, not truncated, with a
// debug diagnostic. These metadata limits do not change the flag's value.
//
// Go retains its existing dd.allocation.key metadata name. Java uses allocationKey.
// Both providers expose __dd_split_serial_id and __dd_feature.<key> metadata.
//
// Set DD_FEATURE_FLAGS_EXPOSURES_DATADOG_LOGGING_ENABLED=false before SDK
// initialization for customer-only exposure logging. The default is true.
// The provider snapshots this setting at construction. The cache and customer
// hooks remain active when it is false. Evaluation events, metrics, span
// enrichment, and flag values are unchanged.
//
// Register the Datadog provider directly with OpenFeature. Exposure hooks run
// in Finally, after all application After hooks. Callbacks
// include errors, non-exposures and repeats. Evaluations with reported errors
// have no sendable exposure and do not consume the cache.
// OpenFeature v1.18.0 propagates application After-hook panics but still runs
// Finally without invoking Error hooks; those evaluations can still consume
// the cache and send an exposure. After hooks should return errors to reject a
// result, allowing the next successful evaluation to emit an exposure.
//
// MultiProvider is not supported for exposure delivery. Its child hooks finish
// before outer application hooks can reject the result. Under MultiProvider,
// Datadog exposure delivery, customer exposure callbacks, and exposure cache
// updates are disabled with a warning. Flag evaluation remains unchanged.
// Upgrade behavior change: earlier versions attempted Datadog exposure delivery
// under MultiProvider. Register the Datadog provider directly to retain delivery.
// Custom provider wrappers are also outside this exposure-hook contract.
//
// OpenFeature Go v1.18.0 can provide zero-value Finally details when a Before hook
// fails or the provider is not ready, even though evaluation returns an error.
// Such callbacks have no sendable exposure. Its evaluation details API can also
// retain success-looking reason, variant, and empty error fields after an After
// hook returns an error, even though the caller receives the default and an error.
// In that case, returned flag metadata can contain __dd_exposure_after_error.
// This temporary internal marker is not a supported application API. Customer
// callbacks receive the rejection message in Details.ErrorMessage instead.
//
// Calling provider resolution methods directly does not run hooks. Register
// customer hooks on the API, client, or evaluation, and retain the provider's hooks.
//
// # Experimentation Opt-Out
//
// Set DD_FEATURE_FLAGS_EXPERIMENTATION_OPT_OUT_MODE=immediate before SDK initialization.
// This enables immediate exclusion separately from evaluation telemetry opt-out:
//
//	provider, err := ddopenfeature.NewDatadogProvider(ddopenfeature.ProviderConfig{})
//	// Handle err and register the provider before evaluating flags.
//	attributes := map[string]any{}
//	// OptOutAt is a customer-owned *time.Time. Nil means not opted out.
//	if user.OptOutAt != nil {
//	    attributes["dd_experimentation_opt_out_at"] = user.OptOutAt.UTC().Format(time.RFC3339Nano)
//	}
//	value, err := client.BooleanValue(ctx, "new-feature", false,
//	    of.NewEvaluationContext(user.ID, attributes))
//
// The zero value, ExperimentationOptOutDefault, uses the environment setting.
// An unset or empty setting defaults to disabled, which ignores the parameter.
// Any explicit non-default mode overrides the environment setting.
// The provider resolves the mode at construction.
// Without an explicit override, an unsupported environment value returns an error
// from NewDatadogProvider. Unsupported enum values also return an error.
// Both enabled modes accept an RFC3339 timestamp string, including fractional seconds.
// Timestamp parsing follows the same rules as allocation startAt and endAt values.
// In immediate mode, any valid timestamp excludes immediately, regardless of its date.
// Omit the attribute to permit experimentation. Null, empty, or malformed values
// return the application default with INVALID_CONTEXT, including for ordinary flags.
// With a ready provider and valid targeting key, the SDK validates the attribute
// before flag lookup, so invalid input takes precedence over FLAG_NOT_FOUND.
// It does not infer a preference from invalid input or continue to an ordinary fallback.
// Check the evaluation error to detect integration mistakes immediately. Supply a
// valid timestamp to opt out, or omit the attribute to permit experimentation.
// Supply the attribute on each evaluation. The SDK does not persist preference history.
//
// Immediate mode skips allocations whose doLog value is true or omitted, before
// targeting and randomization. This includes experiments enrolling 100% of subjects
// and exposure-logging holdouts. It continues through ordinary doLog=false allocations
// in order. If none match, it returns the application's default, not a forced control variant.
// Eligible subjects retain their existing hash buckets; exclusion does not rebalance them.
//
// Winner rollouts with doLog=true are also skipped. Ordinary delivery requires a
// matching doLog=false allocation in the configuration received by the SDK.
// Immediate withdrawal can change an enrolled subject's experience without notifying
// experiment analysis. Customers must account for that change in their analysis.
// No withdrawal event is emitted. Evaluation telemetry remains enabled unless separately
// suppressed with the evaluation telemetry opt-out parameter.
//
// To continue older experiments, set the environment value to "future_experiments_only"
// or configure ExperimentationOptOutFutureExperimentsOnly. Use the same subject attribute.
//
//	provider, err := ddopenfeature.NewDatadogProvider(ddopenfeature.ProviderConfig{
//	    ExperimentationOptOutMode: ddopenfeature.ExperimentationOptOutFutureExperimentsOnly,
//	})
//
// This mode skips exposure-logging allocations activated at or after the supplied
// cutoff. Allocations without randomizationActivatedAt are also skipped. Older
// allocations continue normal evaluation and exposure logging. Ordinary feature
// allocations remain eligible. A malformed randomizationActivatedAt returns the
// application's default with PARSE_ERROR for the affected flag only in future mode
// with a valid opt-out timestamp, including when the malformed allocation is unreachable.
// Disabled mode, immediate mode, and subjects without the attribute ignore invalid
// activation metadata. Missing or null activation remains distinct and skips the
// experiment in future mode. Other valid flags remain available. Malformed startAt
// or endAt still rejects the affected flag in every mode. The comparison uses instants,
// including timezone offsets and fractional seconds, not configuration refresh time
// or allocation schedules.
//
// The cutoff is not an enrollment record: an older experiment can still assign a
// subject for the first time. Configuration edits do not freeze targeting or values.
// Holdouts use the same activation cutoff. Supply the time the subject opted out.
// A later cutoff keeps experiments activated before that later time eligible,
// including experiments started after the subject opted out. Targeting and schedules
// still apply. Remove the attribute to opt back in.
//
// This mode requires activation metadata from the configuration producer. Legacy
// allocations without it are skipped for opted-out subjects, not treated as older.
// Until the producer supplies it, this mode behaves like immediate for
// exposure-logging allocations.
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
// # Debug Logging
//
// Set DD_TRACE_DEBUG=true and call tracer.Start before creating the provider
// to log, at debug level, each step of its setup and every flag evaluation.
// Alternatively, use tracer.Start(tracer.WithDebugMode(true)). Setting
// OTEL_LOG_LEVEL=debug also enables these logs. The provider
// does not change the shared log level. Enabling debug logging after provider
// construction also enables subsequent evaluation logs.
// Every such line starts with "openfeature:",
// so they can be filtered from other tracer output. It covers:
//
//   - The selected configuration source, or that feature flagging is disabled
//   - Each Agentless poll attempt and its outcome: the HTTP status, the
//     transport error, or the reason a response was malformed, plus any retry
//     delay
//   - Each configuration applied, with its flag count, each flag rejected as
//     invalid and why, its createdAt, the SDK's DD_ENV (sdk_env), and the
//     environment the configuration names (config_env). For Agentless, it
//     also logs the ETag and the environment requested from the endpoint
//     (requested_env): a custom endpoint never receives DD_ENV.
//   - Initialization start and its outcome, with the failure reason
//   - Lifecycle changes: recovery after a failed initialization, configuration
//     becoming unavailable, and shutdown, including whether pending exposure
//     events were sent or dropped
//   - Every flag the provider resolves, with its value, the default value,
//     variant, reason, allocation, error, and the evaluation context it
//     resolved against: the targeting key and the other attributes
//   - Every evaluation failure, with the evaluation's default value and
//     context. This includes failures the provider never sees, such as
//     evaluating before it is ready, and an application hook returning an
//     error after the provider resolved the flag
//   - Each exposure event queued for delivery, or skipped with the reason,
//     and each batch dropped after failed delivery
//
// A failure line does not say which value the application received, since
// that depends on how the flag was evaluated. Typed evaluations and
// ObjectValue return the default value. ObjectValueDetails keeps the resolved
// value after an After hook fails, and a multi-provider may return the
// resolved value or another provider's result.
//
// Initialization-failure diagnostics are local logs and do not report errors to
// telemetry.
//
// Credentials and the endpoint's path and query are never logged; only the
// endpoint's scheme and host are logged. Debug logging is meant to explain an
// evaluation result, so it logs the raw evaluation inputs and outputs: the
// targeting key, context attributes, and flag values, in full. Enable it only
// where these may appear in your logs. Each provider logs a one-time warning
// about this, at construction or, if debug logging is enabled later, before
// its first such line. An error returned by an application
// hook is logged with its message as is. Scalar values are logged directly
// and other values as JSON. A value that cannot be encoded, including a
// default value whose MarshalJSON method fails or panics, is logged as
// <unserializable> without affecting the evaluation. When debug logging is
// off, evaluations skip formatting these details.
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
//     HTTPS on an interval. No Agent dependency. Requires DD_API_KEY unless a
//     custom endpoint is configured; DD_SITE optionally selects the managed
//     endpoint's host and has no effect without DD_API_KEY. See
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
//     to the Datadog agent. Defaults to 1 second if not specified. Exposure events
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
//   - DD_FEATURE_FLAGS_EVALUATION_TELEMETRY_PER_SUBJECT_OPT_OUT_ENABLED: Default
//     "false". Set to "true" to enable the dd_evaluation_telemetry_opt_out context
//     parameter. This setting does not globally disable evaluation telemetry.
//     An explicit ProviderConfig.EnableEvaluationTelemetryOptOutParameter overrides it.
//
//   - DD_FEATURE_FLAGS_EXPOSURES_DATADOG_LOGGING_ENABLED: Default "true". Set to
//     "false" to disable only Datadog exposure delivery. The provider snapshots
//     this setting at construction. Customer hooks and exposure deduplication
//     remain active. Evaluation telemetry and flag values are unchanged.
//
//   - DD_FEATURE_FLAGS_EXPERIMENTATION_OPT_OUT_MODE: Default "disabled". Set to
//     "immediate" or "future_experiments_only" to enable the
//     dd_experimentation_opt_out_at context parameter.
//     Explicit ProviderConfig.ExperimentationOptOutMode values other than Default
//     override this setting. Unsupported values return a provider-construction error.
//
//   - DD_EXPERIMENTAL_FLAGGING_PROVIDER_INITIALIZATION_TIMEOUT_MS: Timeout in
//     milliseconds to wait for the first configuration before returning. Used by
//     Init, and by InitWithContext when the caller's context carries no deadline
//     (a context with its own deadline keeps it). This covers the OpenFeature
//     SDK's SetProviderAndWait, which calls InitWithContext with a background
//     context. Expiration returns a PROVIDER_NOT_READY initialization error;
//     delivery continues and a later configuration transitions the provider to
//     ready. Default 10000. An out-of-range (<= 0, or large enough to overflow
//     when converted to a time.Duration) or unparseable value falls back to the
//     default rather than being clamped.
//
// Example (Agentless, the default):
//
//	export DD_API_KEY=<your API key>
//	export DD_EXPERIMENTAL_FLAGGING_PROVIDER_SPAN_ENRICHMENT_ENABLED=true
//
// Standard Datadog environment variables also apply:
//
//   - DD_API_KEY: Required for the default, managed Agentless endpoint.
//   - DD_SITE: Datadog site (default: datadoghq.com). Determines the managed
//     Agentless endpoint's host.
//   - DD_AGENT_HOST: Datadog agent host (default: localhost). Only relevant to
//     the remote_config source.
//   - DD_TRACE_AGENT_PORT: Datadog agent port (default: 8126). Only relevant to
//     the remote_config source.
//   - DD_SERVICE: Service name for tagging
//   - DD_ENV: Environment name (e.g., production, staging)
//   - DD_VERSION: Application version
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
//   - Cache value: allocation key, variant, and optional split serial ID
//   - Capacity: 65536 entries (2^16, ~6.5MB max memory)
//
// Deduplication behavior:
//
//   - Same subject evaluating the same flag multiple times: 1 exposure (deduplicated)
//   - Different subjects evaluating the same flag: 1 exposure per subject
//   - Same subject with variant change (A→B→A): 3 exposures (each change tracked)
//   - Same subject with allocation change: new exposure generated
//   - Same subject with split serial ID change: new exposure generated
//   - Serial ID zero is distinct from an absent serial ID
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
