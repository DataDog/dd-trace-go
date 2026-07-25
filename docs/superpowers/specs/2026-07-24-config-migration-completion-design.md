# Config migration completion design

## Goal

`make config-audit` must produce no findings in the production root module. Every
configuration read covered by that command must go through `internal/config`
without changing its source precedence, sampling time, parsing, error behavior,
programmatic override behavior, or telemetry treatment.

An empty report is necessary, but it is not sufficient by itself. The audit must
also fail when it cannot prove coverage.

## Baseline

The audit on `fa297ea3f` reports:

- 78 migrated configuration keys
- 107 unmigrated keys at 128 call sites
- 28 migrated keys still read directly at 74 call sites
- 0 untracked keys
- 202 recognized direct-read call sites

The scanner undercounts reads today. It does not reject package-load errors,
does not cover every production build variant, accepts suppressions, and misses
several read forms.

## Scope

The `make config-audit` contract covers production packages in the root module,
`github.com/DataDog/dd-trace-go/v2`.

This repository also contains standalone modules for integrations, tools,
fixtures, and smoke tests. They cannot all import the root module's
`internal/config` package. The audit will enumerate these module boundaries and
state that it did not traverse them. Existing exported adapters in
`instrumentation/env` and `instrumentation/options` remain available to those
modules. Root-module callers of those adapters must migrate.

Raw environment reads are permitted only in these low-level boundaries:

- `internal/env` implementation functions
- `internal/config/provider` source lookup functions
- `internal/config/bootstrap.TelemetryEnabled`
- `internal/config/bootstrap.resolveAppSecStackTrace`
- the exported adapter implementations in `instrumentation/env`
- `instrumentation/options.GetBoolEnv`

The audit rejects raw reads everywhere else, including ad hoc reads elsewhere
inside `internal/config`. The AppSec bootstrap boundary is an exact,
function-scoped exception rather than a suppression: standalone import graphs
reach `internal/stacktrace` through `instrumentation/errortrace` and
`internal/telemetry` without importing `internal/config`, while
`internal/config` already depends transitively on stack traces. Installing the
normal provider would therefore either miss those imports or introduce a
cycle. The bootstrap function is the migrated owner for its two registered,
environment-only package-init keys; it caches one raw snapshot, preserves the
legacy diagnostics, and retains metadata for one later telemetry projection.

## Configuration definitions and consumer bindings

A configuration name does not have one universal meaning. `DD_SERVICE`, for
example, is sampled separately by tracer construction, profiler start, OTel
constructors, CI Visibility, OpenFeature, and naming-schema initialization.
Some consumers use stable configuration precedence, while others intentionally
read only the environment.

The registry therefore has two layers.

### Raw definitions

A raw definition records the default and maximum source set plus the telemetry
policy for one source key:

```go
type SourcePolicy uint8

const (
	SourceEnvironment SourcePolicy = iota
	SourceStable
)

type TelemetryPolicy uint8

const (
	TelemetryReport TelemetryPolicy = iota
	TelemetryRedact
	TelemetrySanitizeURL
	TelemetryOmit
)

type RawDefinition struct {
	Key       string
	Sources   SourcePolicy
	Telemetry TelemetryPolicy
}
```

`SourceStable` preserves this order:

1. managed stable configuration
2. Datadog environment variable
3. mapped OTel environment variable
4. local stable configuration
5. hard-coded default

`SourceEnvironment` reads only the Datadog or OTel environment key named by the
binding. Existing environment-only call sites stay environment-only.

### Consumer bindings

A consumer binding records how and when one consumer interprets one or more raw
definitions:

```go
type SamplingBoundary uint8

const (
	SamplePackageInit SamplingBoundary = iota
	SampleTracerConstruction
	SampleProductStart
	SampleConstructor
	SampleFirstUse
	SamplePerCall
)

type ConsumerBinding struct {
	ID              string
	Consumer        string
	Keys            []string
	Sampling        SamplingBoundary
	EnvironmentOnly bool
}
```

Parsers and derivations are attached to typed binding declarations rather than
selected from a universal parser by key. This preserves cases such as:

- `DD_APM_TRACING_ENABLED`, stable precedence in the tracer and environment-only
  parsing in AppSec
- `DD_PROFILING_ENABLED`, where `auto` has separate meaning
- `DD_CIVISIBILITY_ENABLED`, where `parent` is valid
- AppSec durations, rates, explicit empty values, and invalid-value warnings
- OTel integer-millisecond values and signal-specific fallback
- HTTP query-string allowlists and regular-expression fallbacks

The audit maps each config accessor or snapshot field to a consumer binding. A
raw definition alone does not count as a migration.

## Resolution result

Source lookup functions return `(raw string, present bool)`. Explicit empty
values are not collapsed into absence.

Resolution keeps the winning value separate from every attempted source:

```go
type SourceAttempt struct {
	Raw      string
	Present  bool
	Valid    bool
	Err      error
	Origin   Origin
	ConfigID string
}

type Winner[T any] struct {
	Value       T
	Origin      Origin
	ConfigID    string
	DefaultUsed bool
}

type Resolved[T any] struct {
	Winner   Winner[T]
	Attempts []SourceAttempt
}
```

An invalid higher-priority value and a valid lower-priority winner are both
retained. This lets callers preserve diagnostics and telemetry while still
falling back exactly as they do now.

Snapshots copy maps, slices, URLs, and other mutable values. A snapshot is
created again at its declared sampling boundary. Snapshots are not cached
across product restarts unless the old code already used `sync.Once`.

## Store and tracer generations

The package owns one stable `Store`, but each running tracer retains a
generation-pinned `*Config`.

```text
resolve staged generation
        |
apply tracer options
        |
prepare claims and revert existing conflicts
        |
construct tracer components
        |
publish generation and claims at handoff
        |
old tracer continues with its retired generation
```

`NewTracerGeneration` returns an unpublished config. Options and remote-config
handles bind to that config. A failed construction does not change the
published generation.

`Get` keeps its current non-nil contract. When there is no published generation,
it lazily resolves a baseline tracer generation and publishes it with a
double-check under the store lock. Targeted package-init bindings do not call
`Get`, so they do not sample unrelated settings early.

`PrepareClaims` checks staged tracer overrides against active claims and reverts
existing conflicts to the staged generation's source baseline. It records the
claim revision. `PublishTracerGeneration` checks the revision again under the
store lock. A new conflicting claim acquired between prepare and publish makes
publication fail. This prevents construction with one value followed by
publication with another.

A running tracer and its remote-config callbacks continue to read and update
their pinned generation after a replacement is published. They cannot mutate
the next generation.

## Cross-product claims

Claims live in `Store`, separate from the config value that a product uses.
First-in-wins remains the rule for programmatic settings shared by products.

- Tracer claims are staged, checked, and committed with a successful tracer
  generation.
- A failed tracer construction discards its staged claims.
- Replacing a tracer releases the previous tracer's claims only when the new
  generation is published.
- Profiler options remain local to one profiler start, but shared options such
  as service, environment, version, site, agent URL, and tags must acquire
  store claims before they are applied.
- Profiler claims start at a successful `Start` and are released on a later
  startup failure or `Stop`.
- OTel programmatic options participate only where the current public behavior
  already shares a setting.

Conflict telemetry contains the configuration name and product names. It never
contains either value.

## Sampling boundaries

Each old read keeps its boundary.

### Package initialization

Naming schema, AppSec stack-trace defaults, seelog behavior, and other existing
init-time settings use targeted binding resolvers. They do not construct the
tracer config or resolve unrelated product settings.

### Tracer construction

Tracer settings and the tracer view of stable configuration are resolved for
each staged generation. The published config changes only after successful
handoff.

### Product start or constructor

Profiler settings resolve for each `profiler.Start`. AppSec, OTel log, OTel
metric, public propagator, OpenFeature, and integration configuration resolve
when their existing constructors or start paths currently read the environment.
Programmatic options apply to local copies after source resolution.

### First use

CI Visibility settings that currently use `sync.Once` keep that behavior. Test
reset hooks reset the binding cache as well as the consumer cache.

### Per call

`DD_TRACE_128_BIT_TRACEID_LOGGING_ENABLED` remains dynamic at span formatting.
The accessor is owned by `internal/config`, resolves only that binding, and
uses bounded telemetry reporting.

## Telemetry

Provider resolution returns a local slice of telemetry events. It does not call
the telemetry package.

The caller drains events after config publication and after releasing config
and store locks. Product snapshot constructors drain their own local events
after resolution.

The reporter is bounded and deduplicated by binding, generation, source
attempt, and the binding's cadence:

- `Never`
- `OncePerGeneration`
- `OnChange`

Disabled telemetry drops events instead of queuing them. There is no
process-wide unbounded event queue.

`DD_INSTRUMENTATION_TELEMETRY_ENABLED` is a bootstrap exception. The leaf
package `internal/config/bootstrap` reads and caches it with the same
`sync.Once` behavior as the existing telemetry implementation. Both
`internal/config/provider` and `internal/telemetry` may import the leaf. This
bootstrap read does not report itself.

The two AppSec stack-trace package-init keys use the other exact bootstrap
boundary. The bridge consumes their cached snapshot even when
`internal/config` is absent from the import graph. When `internal/config`
initializes, it atomically claims and projects that snapshot once through the
registered binding. The raw reads, parse diagnostics, and telemetry projection
therefore each occur at most once.

Sensitive values use an explicit policy:

- API keys, app keys, and OTel headers are omitted.
- Git repository URLs are sanitized before reporting.
- rules, regular expressions, tag strings, and similar user payloads are
  omitted or reduced to non-value state.
- conflict metrics never contain values.

Setters report after releasing `Config.mu`. Dynamic config callbacks must also
avoid reporting while holding config locks.

## OTel log and metric snapshots

Logs and metrics use separate snapshots. Each contains the raw generic and
signal-specific values needed to reproduce the current fallback behavior.

The constructors preserve:

- signal-specific endpoint, protocol, headers, and timeout over generic values
- current endpoint cleanup and `/v1/logs` or `/v1/metrics` derivation
- integer-millisecond timeout and batch processor parsing
- OTel resource attribute and Datadog tag merge rules
- metrics temporality selection time
- current SDK environment handling where the SDK is intentionally the parser
- explicit user options as the final override

Headers are never registered as configuration telemetry.

## Audit behavior

The scanner has two passes.

### Syntax pass

Every production `.go` file in the root module is parsed without applying build
constraints. This catches read shapes in files for another platform or tag.
The pass detects:

- `internal/env` and instrumentation env calls
- `internal` typed helpers
- stable-config functions and source object methods
- `os.Getenv` and `os.LookupEnv`
- function aliases and local wrapper functions
- unresolved and dynamic expressions that could form `DD_*`, `DD-*`, or
  `OTEL_*` keys
- `nolint:configaudit`

Only the exact low-level raw-read functions listed in the Scope section are
allowed.

### Type and build pass

Package loading must succeed for the supported matrix:

- host `darwin/arm64`
- `linux/amd64` with cgo disabled
- `windows/amd64` with cgo disabled
- production AppSec and other custom tags used by repository CI

Package errors are fatal. The scanner records nested module boundaries and
checks that the root-module package count is nonzero and stable in tests.

### Result contract

The JSON result has no findings only when all of these are empty:

- unmigrated
- migrated but still read outside config
- untracked
- unresolved dynamic reads
- suppressions
- package or variant coverage errors
- consumer bindings without a valid raw definition
- migrated keys without a consumer binding

The table renderer prints nothing for a clean result. CI changes from
non-blocking reporting to a failing check.

## Migration groups

Implementation proceeds in these groups:

1. Audit fail-closed behavior and registry proof
2. Provider result model, source attempts, reporting policy, and telemetry
   bootstrap
3. Store, generations, claims, and tracer construction handoff
4. Instrumentation, naming schema, HTTP, GraphQL, and tracer residual reads
5. Root helpers, global config, telemetry, hostname, process tags, remote
   config, and Git metadata
6. AppSec and API Security
7. CI Visibility and test optimization
8. Profiler
9. OTel log and metric exporters
10. OpenFeature and remaining root-module integrations

Each group starts with behavior tests for source, sampling, invalid input,
restart, and programmatic overrides. Its product tests and the audit run before
the next group begins.

## Acceptance checks

At completion:

```sh
(cd scripts/configaudit && GOWORK=off go test ./...)

(cd scripts/configaudit && \
  GOWORK=off go run . -root ../.. -format json > /tmp/config-audit.json)

jq -e '
  ((.unmigrated // []) | length) == 0 and
  ((.untracked // []) | length) == 0 and
  ((.migrated_but_still_read_outside // []) | length) == 0 and
  ((.unresolved // []) | length) == 0 and
  ((.suppressions // []) | length) == 0 and
  ((.coverage_errors // []) | length) == 0
' /tmp/config-audit.json

make config-audit > /tmp/config-audit.txt
test ! -s /tmp/config-audit.txt

go test -race ./internal/config/... ./ddtrace/tracer ./profiler \
  ./ddtrace/opentelemetry/... ./openfeature ./internal/...

make test/unit
make test/contrib
make test/appsec
make test-deadlock
make fix-modules
git diff --check
```

Tracer hot-path benchmarks run against the merge base and the final branch with
the same command, host, and count. Any regression outside normal variance must
be explained or fixed.

## Review record

Three adversarial reviewers covered dependency structure, behavioral
equivalence, and verification. They rejected three earlier drafts. The final
draft added generation fencing, source-attempt history, consumer-specific
bindings, targeted init resolution, telemetry bootstrap isolation, claim
revalidation, and build-variant audit coverage. All three reviewers approved
this design before implementation began.
