# Config Migration Completion Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `make config-audit` produce a provably empty report after every root-module configuration consumer has moved to `internal/config` with its old behavior intact.

**Architecture:** `internal/config` will register raw definitions separately from consumer bindings. Typed resolvers preserve source attempts, presence, invalid input, sampling boundaries, and telemetry policy. A stable store publishes generation-pinned tracer configs only after successful construction; other products use snapshots created at their existing lifecycle boundaries.

**Tech Stack:** Go 1.24+, `go/ast`, `go/types`, `golang.org/x/tools/go/packages`, `sync`, `sync/atomic`, existing Datadog configuration telemetry, `testify`, repository Make targets.

## Global Constraints

- Work only in `/Users/brian.marks/go/src/github.com/DataDog/dd-trace-go-finish-config-migration` on branch `brian.marks/finish-config-migration`.
- Follow `/Users/brian.marks/go/src/github.com/DataDog/dd-trace-go-finish-config-migration/CONTRIBUTING.md`, `README.md`, and the nearest `AGENTS.md`.
- Write a failing test and observe the expected failure before each production change.
- Preserve each consumer's current source precedence, sampling boundary, parsing, defaults, warnings, errors, and option precedence.
- Raw reads are allowed only in the exact low-level functions named in the design.
- Never report API keys, app keys, OTel headers, raw rules, raw regular expressions, or unsanitized repository URLs.
- Do not report configuration or conflict telemetry while holding a store or config lock.
- A running tracer and its remote-config callbacks remain pinned to their generation.
- `Get()` always returns a non-nil published baseline or tracer generation.
- Nested standalone modules remain outside the root-module audit and keep their exported instrumentation adapters.
- Run `gofmt` on changed Go files and `git diff --check` before every task commit.
- Run the mandatory `pre-push-review` skill after all implementation commits and before presenting results.

---

## File map

### Audit

- `scripts/configaudit/scan.go`: type-aware call recognition and fail-closed package handling
- `scripts/configaudit/syntax.go`: build-independent production-file scan
- `scripts/configaudit/modules.go`: root-module and nested-module boundary discovery
- `scripts/configaudit/registry.go`: raw-definition and consumer-binding validation
- `scripts/configaudit/classify.go`: unresolved, suppression, and coverage result buckets
- `scripts/configaudit/main.go`: supported build matrix and exit behavior
- `scripts/configaudit/*_test.go`: fixture, real-repository, registry, and variant tests

### Configuration foundation

- `internal/config/schema/schema.go`: dependency-leaf metadata and event types
- `internal/config/schema/resolved.go`: `SourceAttempt`, `Winner[T]`, and `Resolved[T]`
- `internal/config/definitions.go`: registry population and defensive registry access
- `internal/config/provider/source.go`: `(raw, present)` source interface
- `internal/config/provider/provider.go`: typed resolution with ordered attempts and local events
- `internal/config/provider/*configsource.go`: explicit-empty source lookup
- `internal/config/bootstrap/telemetry.go`: cached telemetry enablement leaf
- `internal/config/reporter.go`: bounded reporting cadence and telemetry policy
- `internal/config/store.go`: generation publication and product claims
- `internal/config/config.go`: generation-pinned tracer state

### Product snapshots

- `internal/config/instrumentation.go`: init, HTTP, GraphQL, naming, and integration bindings
- `internal/config/system.go`: global, telemetry, Git, hostname, process, and remote-config bindings
- `internal/config/appsec.go`: AppSec and API Security snapshot
- `internal/config/civisibility.go`: CI Visibility and test-optimization bindings
- `internal/config/profiler.go`: profiler start snapshot
- `internal/config/otel.go`: OTel log and metric snapshots
- `internal/config/openfeature.go`: OpenFeature snapshot

## Task 1: Make config-audit fail closed

**Files:**

- Create: `scripts/configaudit/syntax.go`
- Create: `scripts/configaudit/modules.go`
- Modify: `scripts/configaudit/scan.go`
- Modify: `scripts/configaudit/classify.go`
- Modify: `scripts/configaudit/main.go`
- Modify: `scripts/configaudit/README.md`
- Modify: `.github/workflows/config-audit.yml`
- Test: `scripts/configaudit/scan_test.go`
- Test: `scripts/configaudit/classify_test.go`
- Test: `scripts/configaudit/testdata/fixture_a/fixture.go`

**Interfaces:**

- Produces: `scanSyntax(root string, allow rawReadAllowlist) ([]Finding, error)`
- Produces: `discoverModules(root string) (rootModule Module, nested []Module, err error)`
- Produces: result fields `Unresolved`, `Suppressions`, and `CoverageErrors`

- [ ] **Step 1: Add failing scanner tests**

Add fixture reads and assertions for direct OS access, aliases, dynamic keys, and
a suppression:

```go
var readEnv = os.Getenv

func exerciseDynamic(prefix string) {
	_ = os.Getenv("DD_DIRECT_OS")
	_ = readEnv("DD_ALIASED_OS")
	_ = envGet("DD_TRACE_" + prefix)
	_ = envGet("DD_SUPPRESSED") //nolint:configaudit
}
```

Assert that literal reads are call sites, the concatenation is unresolved, and
the annotation is a suppression finding rather than an ignored line.

- [ ] **Step 2: Run the tests and observe the expected failures**

Run:

```sh
cd scripts/configaudit
GOWORK=off go test ./... -run 'TestScan_(DirectOS|Alias|Dynamic|Suppression)|TestDiscoverModules'
```

Expected: failures because the result has no unresolved/suppression buckets and
the scanner does not recognize the new forms.

- [ ] **Step 3: Implement the syntax pass and module discovery**

Use `go/parser.ParseFile` with comments for every production `.go` file under
the root module. Stop recursion at nested `go.mod` directories. Resolve local
function aliases from `ValueSpec` and `AssignStmt`. Record a finding when a
recognized raw-read call has a nonconstant key expression. Permit raw reads only
when both the relative file and enclosing function match the allowlist.

The allowlist type must be exact:

```go
type rawReadLocation struct {
	File string
	Func string
}

type rawReadAllowlist map[rawReadLocation]struct{}
```

- [ ] **Step 4: Reject package errors and suppressions**

Check `packageErrors(pkgs)` in `scan`. Add these JSON fields:

```go
type AuditResult struct {
	Migrated                    []ConfigEntry `json:"migrated"`
	Unmigrated                  []ConfigEntry `json:"unmigrated"`
	Untracked                   []ConfigEntry `json:"untracked"`
	MigratedButStillReadOutside []ConfigEntry `json:"migrated_but_still_read_outside"`
	Unresolved                  []Finding     `json:"unresolved"`
	Suppressions                []Finding     `json:"suppressions"`
	CoverageErrors              []string      `json:"coverage_errors"`
}
```

Do not skip suppressed calls. Put the annotation in `Suppressions` and classify
the call normally when its key is resolvable.

- [ ] **Step 5: Add build-variant coverage**

Load the root packages with these environments:

```go
var buildVariants = []buildVariant{
	{Name: "host"},
	{Name: "linux-amd64", Env: []string{"GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=0"}},
	{Name: "windows-amd64", Env: []string{"GOOS=windows", "GOARCH=amd64", "CGO_ENABLED=0"}},
	{Name: "linux-amd64-appsec", Env: []string{"GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=0"}, BuildFlags: []string{"-tags=appsec"}},
}
```

Append variant package errors to `CoverageErrors`. The syntax pass remains the
coverage mechanism for files that cannot be type-loaded on the host.

- [ ] **Step 6: Make CI fail on findings**

Add `AuditResult.Clean() bool`. Make `run` return a typed nonclean error after
rendering when any failure bucket is nonempty. Update the workflow text and
remove the statement that the check is non-blocking.

- [ ] **Step 7: Run and commit**

Run:

```sh
(cd scripts/configaudit && GOWORK=off go test ./...)
gofmt -w $(git diff --name-only --diff-filter=ACM -- '*.go')
git diff --check
git add scripts/configaudit .github/workflows/config-audit.yml
git commit -m "chore(config): make configuration audit fail closed"
```

Expected: audit unit tests pass. The repository audit itself remains red with
the migration inventory.

## Task 2: Register raw definitions and consumer bindings

**Files:**

- Create: `internal/config/schema/schema.go`
- Create: `internal/config/schema/resolved.go`
- Create: `internal/config/definitions.go`
- Create: `scripts/configaudit/registry.go`
- Modify: `scripts/configaudit/migrated.go`
- Test: `internal/config/definitions_test.go`
- Test: `scripts/configaudit/migrated_test.go`

**Interfaces:**

- Produces: `RawDefinition`, `ConsumerBinding`, `RegisteredDefinitions()`
- Produces: `RegisterRaw`, `RegisterBinding`, and registry validation
- Consumes: Task 1 audit failure buckets

- [ ] **Step 1: Add failing registry tests**

Test duplicate binding IDs, bindings with missing raw keys, shared keys with two
sampling boundaries, and a raw key without a binding:

```go
func TestRegistryAllowsConsumerSpecificBindings(t *testing.T) {
	r := newRegistry()
	r.addRaw(RawDefinition{Key: "DD_SERVICE", Sources: SourceStable})
	r.addBinding(ConsumerBinding{
		ID: "tracer.service", Consumer: "tracer",
		Keys: []string{"DD_SERVICE"}, Sampling: SampleTracerConstruction,
	})
	r.addBinding(ConsumerBinding{
		ID: "profiler.service", Consumer: "profiler",
		Keys: []string{"DD_SERVICE"}, Sampling: SampleProductStart,
	})
	require.NoError(t, r.validate())
}
```

- [ ] **Step 2: Run and observe missing-type failures**

Run:

```sh
go test ./internal/config -run '^TestRegistry'
(cd scripts/configaudit && GOWORK=off go test ./... -run '^TestLoadMigrated')
```

Expected: compile failure because registry types do not exist.

- [ ] **Step 3: Implement the metadata registry**

Define the enums and structs from the design in the dependency-leaf
`internal/config/schema` package so `provider` and `config` can both import
them. Registry mutation occurs only during `internal/config` package
initialization. `RegisteredDefinitions` returns sorted defensive copies.

Add typed result types:

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

- [ ] **Step 4: Change migration proof**

Replace the `loadConfig` body heuristic. Parse calls to `registerRaw` and
`registerBinding` in `internal/config`, resolve constant keys and binding IDs,
and validate that every migrated key has at least one binding.

- [ ] **Step 5: Run and commit**

Run:

```sh
go test ./internal/config -run '^TestRegistry'
(cd scripts/configaudit && GOWORK=off go test ./...)
gofmt -w $(git diff --name-only --diff-filter=ACM -- '*.go')
git diff --check
git add internal/config/schema internal/config/definitions.go scripts/configaudit
git commit -m "feat(config): register configuration consumer bindings"
```

## Task 3: Preserve source attempts and explicit empty values

**Files:**

- Create: `internal/config/provider/source.go`
- Modify: `internal/config/provider/provider.go`
- Modify: `internal/config/provider/envconfigsource.go`
- Modify: `internal/config/provider/declarativeconfigsource.go`
- Modify: `internal/config/provider/otelenvconfigsource.go`
- Test: `internal/config/provider/provider_test.go`
- Test: `internal/config/provider/declarativeconfigsource_test.go`

**Interfaces:**

- Produces: `provider.Resolve[T](p *Provider, def schema.RawDefinition, defValue T, parse schema.Parser[T]) schema.Resolved[T]`
- Produces: `LookupSource.lookup(key string) (raw string, present bool)`
- Produces: local `[]ConfigEvent` without telemetry calls

- [ ] **Step 1: Add failing provider tests**

Cover explicit empty and invalid high-priority fallthrough:

```go
func TestResolveKeepsAllSourceAttempts(t *testing.T) {
	p := providerForTest(
		source(OriginLocalStableConfig, "12", true),
		source(OriginEnvVar, "7", true),
		source(OriginManagedStableConfig, "invalid", true),
	)
	got := Resolve(p, intDefinition("DD_VALUE"), 3, parseInt)
	require.Equal(t, 7, got.Winner.Value)
	require.Equal(t, OriginEnvVar, got.Winner.Origin)
	require.Len(t, got.Attempts, 3)
	require.False(t, got.Attempts[2].Valid)
	require.Error(t, got.Attempts[2].Err)
}
```

Add a separate test where the managed source returns `("", true)`.

- [ ] **Step 2: Run and observe the expected failures**

Run:

```sh
go test ./internal/config/provider -run 'TestResolve|TestDeclarative.*Empty'
```

Expected: tests fail because sources collapse empty values and provider getters
do not return attempts.

- [ ] **Step 3: Implement source lookup and resolution**

Change `configSource.get` to:

```go
lookup(key string) (raw string, present bool)
```

Resolve sources from lowest to highest priority so all attempts and telemetry
events retain source order while the last valid value wins. `ConfigEvent` must
contain the binding ID, name, transformed value state, origin, config ID, and
cadence. Do not import `internal/telemetry` from the generic resolution loop
except for the existing origin alias.

- [ ] **Step 4: Keep compatibility getters temporarily**

Implement existing `GetString`, `GetBool`, `GetInt`, `GetFloat`, `GetDuration`,
and `GetMap` in terms of `Resolve`. This keeps migrated callers compiling while
later tasks move them to named bindings.

- [ ] **Step 5: Run and commit**

Run:

```sh
go test ./internal/config/provider ./internal/config
gofmt -w $(git diff --name-only --diff-filter=ACM -- '*.go')
git diff --check
git add internal/config/provider internal/config
git commit -m "feat(config): preserve configuration source attempts"
```

## Task 4: Isolate telemetry bootstrap and reporting

**Files:**

- Create: `internal/config/bootstrap/telemetry.go`
- Create: `internal/config/bootstrap/telemetry_test.go`
- Create: `internal/config/reporter.go`
- Create: `internal/config/reporter_test.go`
- Modify: `internal/config/configtelemetry/configtelemetry.go`
- Modify: `internal/telemetry/globalclient.go`
- Modify: `internal/telemetry/globalclient_test.go`
- Modify: `internal/config/config.go`

**Interfaces:**

- Produces: `bootstrap.TelemetryEnabled() bool`
- Produces: `Reporter.Report(events []ConfigEvent, generation uint64)`
- Produces: `Reporter.ResetForTesting()`

- [ ] **Step 1: Add failing bootstrap and reporter tests**

Test cached first read, disabled-drop behavior, once-per-generation
deduplication, on-change reporting, redaction, URL sanitization, and bounded map
size:

```go
func TestReporterNeverStoresSensitiveValues(t *testing.T) {
	r, sink := newTestReporter()
	r.Report([]ConfigEvent{{
		BindingID: "profiler.api-key",
		Name: "DD_API_KEY",
		Value: "secret",
		Policy: TelemetryOmit,
		Cadence: ReportOncePerGeneration,
	}}, 1)
	require.Empty(t, sink.Events())
	require.NotContains(t, fmt.Sprint(r.state), "secret")
}
```

- [ ] **Step 2: Run and observe missing APIs**

Run:

```sh
go test ./internal/config/bootstrap ./internal/config -run 'TestReporter|TestTelemetryEnabled'
go test ./internal/telemetry -run '^TestGlobalTelemetryEnabled'
```

Expected: compile failure for the new packages and reporter.

- [ ] **Step 3: Implement bootstrap caching**

Move the existing once-cached boolean read to
`internal/config/bootstrap.TelemetryEnabled`. `telemetry.Disabled` returns the
negation. Keep a test-only reset function in the bootstrap package and call it
from telemetry tests.

- [ ] **Step 4: Implement bounded reporting**

Reporter state is a map keyed by:

```go
type reportKey struct {
	BindingID string
	Generation uint64
	Origin Origin
	ConfigID string
}
```

For on-change bindings, store only the last transformed value hash per binding.
The number of entries must be bounded by registered bindings times supported
source attempts. Drop events immediately when bootstrap says telemetry is
disabled.

- [ ] **Step 5: Move setter reports outside locks**

For every existing `Set*` method, capture the event while locked, unlock, then
submit it. Remove values from product-conflict metric tags. Add a lock-reentry
test whose telemetry sink calls a config getter.

- [ ] **Step 6: Run and commit**

Run:

```sh
go test -race ./internal/config/... ./internal/telemetry/...
gofmt -w $(git diff --name-only --diff-filter=ACM -- '*.go')
git diff --check
git add internal/config internal/telemetry
git commit -m "fix(config): decouple resolution from telemetry reporting"
```

## Task 5: Add staged tracer generations and store claims

**Files:**

- Create: `internal/config/store.go`
- Create: `internal/config/store_test.go`
- Modify: `internal/config/config.go`
- Modify: `internal/config/README.md`
- Modify: `ddtrace/tracer/option.go`
- Modify: `ddtrace/tracer/tracer.go`
- Test: `ddtrace/tracer/option_test.go`
- Test: `ddtrace/tracer/tracer_test.go`

**Interfaces:**

- Produces: `NewTracerGeneration() *Config`
- Produces: `(*Config).PrepareClaims() PreparedClaims`
- Produces: `PublishTracerGeneration(*Config, PreparedClaims) error`
- Produces: `AcquireProductClaims(Product, []Claim) (release func(), accepted map[string]bool)`
- Preserves: `Get() *Config`

- [ ] **Step 1: Add failing lifecycle and race tests**

Cover non-nil first `Get`, failed first construction, retired generation reads,
late retired RC mutation, and a profiler claim acquired between prepare and
publish:

```go
func TestPublishRejectsClaimRevisionConflict(t *testing.T) {
	staged := NewTracerGeneration()
	staged.SetServiceName("tracer", OriginCode, ProductTracer)
	prepared := staged.PrepareClaims()
	release, accepted := AcquireProductClaims(ProductProfiler, []Claim{{
		Name: "DD_SERVICE", Value: "profiler",
	}})
	defer release()
	require.True(t, accepted["DD_SERVICE"])
	require.Error(t, PublishTracerGeneration(staged, prepared))
}
```

- [ ] **Step 2: Run and observe lifecycle failures**

Run:

```sh
go test -race ./internal/config -run 'Test(Get|Publish|Retired|Claim)'
go test -race ./ddtrace/tracer -run 'TestStart.*(Failed|Generation|Race)'
```

Expected: missing API failures.

- [ ] **Step 3: Implement Store**

Store holds the current generation, claim revision, active product claims, and
the generation counter under one mutex. `Get` resolves outside the lock, then
publishes with a double-check. Drain resolution telemetry after unlock.

Tracer setters record baseline and staged claim data on their own `Config`.
`PrepareClaims` reverts conflicts already active. Publication rechecks the
revision and fails on a newly conflicting claim.

- [ ] **Step 4: Stage tracer construction**

Change tracer config construction to use an unpublished generation. Apply
options to it, prepare claims, construct dependent components, and publish only
at the current global tracer handoff. Return an error if publication loses a
claim race. Construction errors leave current config and claims unchanged.

- [ ] **Step 5: Update the config documentation**

Replace the optional product variadic guidance with product-bound staging and
claim lifecycle. Document that `Get` follows the current generation while a
running tracer owns its pinned generation.

- [ ] **Step 6: Run and commit**

Run:

```sh
go test -race ./internal/config/... ./ddtrace/tracer
gofmt -w $(git diff --name-only --diff-filter=ACM -- '*.go')
git diff --check
git add internal/config ddtrace/tracer
git commit -m "feat(config): publish generation-pinned tracer configuration"
```

## Task 6: Migrate tracer and instrumentation residual reads

**Files:**

- Create: `internal/config/instrumentation.go`
- Create: `internal/config/instrumentation_test.go`
- Modify: `ddtrace/tracer/option.go`
- Modify: `ddtrace/tracer/otel_dd_mappings.go`
- Modify: `ddtrace/tracer/seelog_leak_workaround.go`
- Modify: `ddtrace/tracer/span.go`
- Modify: `ddtrace/tracer/spancontext.go`
- Modify: `ddtrace/tracer/textmap.go`
- Modify: `instrumentation/graphql/graphql.go`
- Modify: `instrumentation/httptrace/config.go`
- Modify: `instrumentation/instrumentation.go`
- Modify: `instrumentation/internal/namingschema/namingschema.go`
- Modify: `internal/namingschema/namingschema.go`
- Modify: `contrib/cloud.google.com/go/pubsubtrace/config.go`
- Modify: `contrib/confluentinc/confluent-kafka-go/kafkatrace/tracer.go`
- Test: corresponding package tests

**Interfaces:**

- Produces: `HTTPTraceSnapshot() HTTPTraceConfig`
- Produces: `NewPropagationSnapshot() PropagationConfig`
- Produces: `TraceIDLoggingEnabled() bool`
- Produces: targeted naming, GraphQL, PubSub, Kafka, and instrumentation getters

- [ ] **Step 1: Add failing behavior tests**

Add tables for absent, empty, invalid, and valid HTTP query settings; public
propagator environment changes between constructions; span formatting changes
between calls; and naming-schema init behavior in a subprocess.

The per-call assertion must be:

```go
t.Setenv("DD_TRACE_128_BIT_TRACEID_LOGGING_ENABLED", "false")
require.NotContains(t, formatSpan(span), "0000000000000001")
t.Setenv("DD_TRACE_128_BIT_TRACEID_LOGGING_ENABLED", "true")
require.Contains(t, formatSpan(span), "0000000000000001")
```

- [ ] **Step 2: Run targeted tests and observe direct-read behavior**

Run:

```sh
go test ./internal/config ./instrumentation/httptrace ./instrumentation/graphql ./ddtrace/tracer
```

Expected: new config APIs are missing.

- [ ] **Step 3: Implement targeted bindings**

Register each raw key with its old source policy. Define `HTTPTraceConfig` with
presence fields for all three allowlists, baggage keys, and query regexp.
`NewPropagationSnapshot` resolves only propagation keys at each call.
`TraceIDLoggingEnabled` is an environment-only per-call binding with on-change
reporting.

Where an already-registered stable key also has a legacy environment-only
consumer, mark that consumer binding `EnvironmentOnly`. Raw definitions retain
the default and maximum source set; bindings may narrow stable resolution to
the environment but may never widen an environment-only raw definition.

- [ ] **Step 4: Replace consumers and remove suppressions**

Replace every listed direct read with the matching snapshot field or accessor.
Delete both `nolint:configaudit` annotations in `span.go` and the suppression in
`spancontext.go`. Keep stopped-tracer environment fallback behavior through a
dedicated binding rather than the tracer-effective config.

- [ ] **Step 5: Run audit and commit**

Run:

```sh
go test ./ddtrace/tracer ./instrumentation/... ./internal/namingschema \
  ./contrib/cloud.google.com/go/pubsubtrace \
  ./contrib/confluentinc/confluent-kafka-go/kafkatrace
make config-audit > /tmp/config-audit-task6.txt || true
! rg '^PACKAGE: (ddtrace/tracer|instrumentation|internal/namingschema|contrib/cloud.google.com/go/pubsubtrace|contrib/confluentinc/confluent-kafka-go/kafkatrace)$' /tmp/config-audit-task6.txt
gofmt -w $(git diff --name-only --diff-filter=ACM -- '*.go')
git diff --check
git add internal/config ddtrace/tracer instrumentation internal/namingschema contrib
git commit -m "refactor(config): migrate tracer and instrumentation reads"
```

Expected: those packages no longer appear in audit output.

## Task 7: Migrate process-wide and telemetry settings

**Files:**

- Create: `internal/config/system.go`
- Create: `internal/config/system_test.go`
- Modify: `internal/agent.go`
- Modify: `internal/env.go`
- Modify: `internal/gitmetadata.go`
- Modify: `internal/globalconfig/globalconfig.go`
- Modify: `internal/hostname/providers.go`
- Modify: `internal/log/log.go`
- Modify: `internal/processtags/processtags.go`
- Modify: `internal/remoteconfig/config.go`
- Modify: `internal/remoteconfig/remoteconfig.go`
- Modify: `internal/telemetry/app_endpoints.go`
- Modify: `internal/telemetry/client_config.go`
- Modify: `internal/telemetry/globalclient.go`
- Modify: `ddtrace/tracer/spancontext.go`
- Modify: `ddtrace/tracer/transport.go`
- Modify: `internal/civisibility/utils/net/client.go`
- Modify: `openfeature/evp.go`
- Modify: `profiler/options.go`
- Test: corresponding package tests

**Interfaces:**

- Produces: `SystemSnapshot`, `TelemetrySnapshot`, `GitMetadataSnapshot`
- Produces: targeted hostname, process-tag, install-info, and remote-config bindings

- [ ] **Step 1: Add failing tests**

Characterize Git URL credential removal, `DD_TAGS` Git metadata overrides,
telemetry interval defaults, agent URL precedence, hostname caching, process-tag
enablement, and remote-config constructor-time sampling.

Assert that a telemetry snapshot contains no API key in its registered events:

```go
snapshot, events := ResolveTelemetrySnapshot()
require.Equal(t, "secret", snapshot.APIKey)
require.NotContains(t, fmt.Sprint(events), "secret")
```

- [ ] **Step 2: Run and observe missing snapshots**

Run:

```sh
go test ./internal/... -run 'Test(System|Telemetry|GitMetadata|RemoteConfig|Hostname|ProcessTags)'
```

Expected: missing snapshot APIs.

- [ ] **Step 3: Implement system bindings**

Move raw reads from root helpers into `internal/config/system.go`. Keep pure
parsers and constants in root `internal`. Sanitize repository URLs before any
event is created. Use environment-only bindings where the removed helper used
`env.Get` or `internal.BoolEnv`.

- [ ] **Step 4: Push values across cycle boundaries**

`internal/globalconfig` and `internal/telemetry` must not import
`internal/config`. Add setters for install metadata and telemetry snapshot
fields. Resolve and push at the same boundary where the old package read them.
The telemetry-enabled flag continues to use the bootstrap leaf.

- [ ] **Step 5: Replace callers and delete env-reading helpers**

Move callers of `AgentURLFromEnv`, `ExternalEnvironment`, and
`GetGitMetadataTags` to config snapshots. Delete only the raw-read behavior;
keep public pure helpers that have other callers.

- [ ] **Step 6: Run audit and commit**

Run:

```sh
go test -race ./internal/... ./ddtrace/tracer ./profiler ./openfeature
make config-audit > /tmp/config-audit-task7.txt || true
! rg '^PACKAGE: (internal|internal/globalconfig|internal/hostname|internal/log|internal/processtags|internal/remoteconfig|internal/telemetry)$' /tmp/config-audit-task7.txt
! rg -n '\b(AgentURLFromEnv|ExternalEnvironment|GetGitMetadataTags)\(' \
  ddtrace internal openfeature profiler --glob '*.go' --glob '!**/*_test.go'
gofmt -w $(git diff --name-only --diff-filter=ACM -- '*.go')
git diff --check
git add internal ddtrace/tracer profiler openfeature
git commit -m "refactor(config): centralize process-wide configuration"
```

## Task 8: Migrate AppSec and API Security

**Files:**

- Create: `internal/config/appsec.go`
- Create: `internal/config/appsec_test.go`
- Modify: `internal/appsec/config/config.go`
- Modify: `internal/appsec/config/internal_config.go`
- Modify: `internal/appsec/listener/httpsec/request.go`
- Modify: `internal/appsec/remoteconfig.go`
- Modify: `internal/stacktrace/stacktrace.go`
- Modify: `instrumentation/appsec/emitter/waf/actions/block.go`
- Modify: `instrumentation/instrumentation.go`
- Test: existing AppSec package tests

**Interfaces:**

- Produces: `ResolveAppSecSnapshot() (AppSecSnapshot, []Diagnostic)`
- Preserves: stable precedence for AppSec enablement and environment-only
  `DD_APM_TRACING_ENABLED` in AppSec

- [ ] **Step 1: Add failing parser and presence tests**

Test explicit-empty `DD_APPSEC_RULES`, invalid enablement, sample-rate clamps,
unitless WAF microseconds, positive rate limits, stack depth, and API Security
message-limit warnings. Include managed-invalid plus environment-valid
fallthrough.

- [ ] **Step 2: Run and observe failures**

Run:

```sh
go test ./internal/config ./internal/appsec/... ./internal/stacktrace \
  -run 'Test(AppSec|APISecurity|StackTrace)'
```

Expected: missing snapshot APIs.

- [ ] **Step 3: Implement AppSec bindings and custom parsers**

`AppSecSnapshot` must carry typed values plus `RulesPresent`,
`TracingEnabledPresent`, origins, and diagnostic errors. Reuse the existing
parser bodies by moving them beside the bindings; do not replace them with
generic duration or boolean parsing.

- [ ] **Step 4: Replace AppSec consumers**

Construct the snapshot at the existing AppSec config boundary. Use the rules
presence bit for remote-config capability suppression. Resolve stack-trace init
settings through a targeted init binding.

- [ ] **Step 5: Run and commit**

Run:

```sh
go test -race ./internal/appsec/... ./internal/stacktrace ./instrumentation
make test/appsec
make config-audit > /tmp/config-audit-task8.txt || true
! rg '^PACKAGE: (internal/appsec|internal/appsec/config|internal/appsec/listener/httpsec|internal/stacktrace|instrumentation)$' /tmp/config-audit-task8.txt
gofmt -w $(git diff --name-only --diff-filter=ACM -- '*.go')
git diff --check
git add internal/config internal/appsec internal/stacktrace instrumentation
git commit -m "refactor(config): migrate AppSec configuration"
```

## Task 9: Migrate CI Visibility and test optimization

**Files:**

- Create: `internal/config/civisibility.go`
- Create: `internal/config/civisibility_test.go`
- Modify: `internal/bazel/mode.go`
- Modify: `internal/civisibility/envconfig/enabled.go`
- Modify: `internal/civisibility/integrations/civisibility.go`
- Modify: `internal/civisibility/integrations/civisibility_features.go`
- Modify: `internal/civisibility/integrations/gotesting/instrumentation.go`
- Modify: `internal/civisibility/integrations/gotesting/instrumentation_orchestrion.go`
- Modify: `internal/civisibility/integrations/logs/logs.go`
- Modify: `internal/civisibility/utils/ci_providers.go`
- Modify: `internal/civisibility/utils/environmentTags.go`
- Modify: `internal/civisibility/utils/file_environmental_data.go`
- Modify: `internal/civisibility/utils/net/client.go`
- Modify: `internal/civisibility/utils/net/coverage_report.go`
- Modify: `internal/civisibility/utils/telemetry/telemetry_count.go`
- Test: corresponding CI Visibility tests

**Interfaces:**

- Produces: `CIVisibilitySnapshot() CIVisibilityConfig`
- Produces: `ResetCIVisibilityForTesting()`
- Preserves: first-use caching and `parent` mode

- [ ] **Step 1: Add failing CI behavior tests**

Cover `parent`, explicit empty session name, retry boundaries, test-management
retries, first-use caching across environment mutation, reset behavior, and
agentless URL/API key handling.

- [ ] **Step 2: Run and observe missing bindings**

Run:

```sh
go test ./internal/civisibility/... ./internal/bazel -run 'Test(CIVisibility|TestOptimization|Bazel)'
```

Expected: missing snapshot APIs.

- [ ] **Step 3: Implement the first-use snapshot**

Use one `sync.Once` for the existing first-use feature group. Keep constructor
reads outside that group as separate bindings. Copy all slices and maps.
Telemetry policies omit API keys and file payload contents.

- [ ] **Step 4: Replace consumers**

Replace direct reads in every listed file. Dynamic CI provider variables that
are not Datadog configuration remain environment reads and are outside the
configuration registry.

- [ ] **Step 5: Run and commit**

Run:

```sh
go test -race ./internal/civisibility/... ./internal/bazel
make config-audit > /tmp/config-audit-task9.txt || true
! rg '^PACKAGE: (internal/bazel|internal/civisibility)' /tmp/config-audit-task9.txt
gofmt -w $(git diff --name-only --diff-filter=ACM -- '*.go')
git diff --check
git add internal/config internal/civisibility internal/bazel
git commit -m "refactor(config): migrate CI Visibility configuration"
```

## Task 10: Migrate profiler start configuration and claims

**Files:**

- Create: `internal/config/profiler.go`
- Create: `internal/config/profiler_test.go`
- Modify: `profiler/options.go`
- Modify: `profiler/profiler.go`
- Modify: `profiler/upload.go`
- Test: `profiler/options_test.go`
- Test: `profiler/profiler_test.go`

**Interfaces:**

- Produces: `ResolveProfilerSnapshot() (ProfilerSnapshot, []ConfigEvent)`
- Consumes: `AcquireProductClaims(ProductProfiler, claims)`

- [ ] **Step 1: Add failing restart, error, and claim tests**

Test environment resampling on every `Start`, `auto`, invalid upload timeout
returning a start error, profiler-first/tracer-first shared options, claim
release on failed start, and claim release on `Stop`.

```go
t.Setenv("DD_SITE", "datadoghq.com")
require.NoError(t, Start())
Stop()
t.Setenv("DD_SITE", "datadoghq.eu")
require.NoError(t, Start())
defer Stop()
require.Equal(t, "datadoghq.eu", activeProfilerConfig().site)
```

- [ ] **Step 2: Run and observe missing snapshot failures**

Run:

```sh
go test -race ./internal/config ./profiler -run 'Test(Profiler|Start.*Env|ProductClaim)'
```

Expected: missing profiler snapshot and claim lifecycle.

- [ ] **Step 3: Implement profiler snapshot**

Use stable precedence only for `DD_PROFILING_ENABLED`, as today. Other profiler
keys and shared service/env/version/site/tags/API key values remain
environment-only. Preserve all existing custom parsers and upload-time errors.

- [ ] **Step 4: Apply local options through claims**

Resolve a fresh local snapshot, apply accepted profiler options in order, then
start the profiler. Acquire shared claims at successful start and release them
on every later failure or stop. Never call `CreateNew` or mutate tracer-effective
config.

- [ ] **Step 5: Run and commit**

Run:

```sh
go test -race ./profiler ./internal/config
make config-audit > /tmp/config-audit-task10.txt || true
! rg '^PACKAGE: profiler$' /tmp/config-audit-task10.txt
gofmt -w $(git diff --name-only --diff-filter=ACM -- '*.go')
git diff --check
git add internal/config profiler
git commit -m "refactor(config): migrate profiler start configuration"
```

## Task 11: Migrate OTel log and metric configuration

**Files:**

- Create: `internal/config/otel.go`
- Create: `internal/config/otel_test.go`
- Modify: `ddtrace/opentelemetry/log/exporter.go`
- Modify: `ddtrace/opentelemetry/log/resource.go`
- Modify: `ddtrace/opentelemetry/log/telemetry.go`
- Modify: `ddtrace/opentelemetry/metric/exporter.go`
- Modify: `ddtrace/opentelemetry/metric/meter_provider.go`
- Modify: `ddtrace/opentelemetry/metric/resource.go`
- Modify: `ddtrace/opentelemetry/metric/telemetry.go`
- Test: existing OTel log and metric tests

**Interfaces:**

- Produces: `ResolveOTelLogSnapshot() OTelLogSnapshot`
- Produces: `ResolveOTelMetricSnapshot() OTelMetricSnapshot`

- [ ] **Step 1: Add failing precedence and parsing tests**

For each signal, cover generic-only, signal-specific, explicit empty, invalid
protocol, endpoint path derivation, headers, timeouts, batch values,
temporality, resource merges, SDK environment delegation, and explicit user
options last.

- [ ] **Step 2: Run and observe missing snapshot failures**

Run:

```sh
go test ./internal/config ./ddtrace/opentelemetry/log ./ddtrace/opentelemetry/metric
```

Expected: missing snapshot APIs.

- [ ] **Step 3: Implement separate signal snapshots**

Keep raw generic and signal-specific resolved values in separate fields. Do not
reuse the tracer's derived OTLP endpoint or headers. Mark every header binding
`TelemetryOmit`.

- [ ] **Step 4: Configure exporters explicitly**

Replace local reads with snapshot fields. Where the current metrics exporter
deliberately lets the upstream SDK read environment, either pass the equivalent
resolved option or preserve the SDK path under an equivalence test. Apply user
options after snapshot-derived options.

- [ ] **Step 5: Run and commit**

Run:

```sh
go test -race ./ddtrace/opentelemetry/... ./internal/config
make config-audit > /tmp/config-audit-task11.txt || true
! rg '^PACKAGE: ddtrace/opentelemetry/' /tmp/config-audit-task11.txt
gofmt -w $(git diff --name-only --diff-filter=ACM -- '*.go')
git diff --check
git add internal/config ddtrace/opentelemetry
git commit -m "refactor(config): migrate OTel signal configuration"
```

## Task 12: Migrate OpenFeature and prove the inventory is exhausted

**Files:**

- Create: `internal/config/openfeature.go`
- Create: `internal/config/openfeature_test.go`
- Modify: `openfeature/exposure.go`
- Modify: `openfeature/flageval_logging.go`
- Modify: `openfeature/provider.go`
- Test: corresponding package tests

**Interfaces:**

- Produces: `ResolveOpenFeatureSnapshot() OpenFeatureSnapshot`
- Consumes: registry and resolver foundation

- [ ] **Step 1: Capture the remaining report**

Run:

```sh
make config-audit > /tmp/config-audit-task12-before.txt || true
(cd scripts/configaudit && GOWORK=off go run . -root ../.. -format json) \
  > /tmp/config-audit-task12.json
```

The only expected findings are the OpenFeature call sites listed in this task.
Any other finding means its owning earlier task is incomplete; return to that
task and its behavior tests before changing OpenFeature.

- [ ] **Step 2: Add failing OpenFeature tests**

Test executable fallback for service, environment-only env/version, feature
booleans, and construction-time resampling.

- [ ] **Step 3: Run and observe failures**

Run:

```sh
go test ./openfeature ./internal/config -run 'TestOpenFeature'
```

Expected: missing snapshot API.

- [ ] **Step 4: Implement and replace remaining consumers**

Create the OpenFeature snapshot with environment-only shared fields and the
existing global service fallback. Replace the direct reads in
`exposure.go`, `flageval_logging.go`, and `provider.go`.

- [ ] **Step 5: Prove an empty audit and commit**

Run:

```sh
(cd scripts/configaudit && GOWORK=off go test ./...)
(cd scripts/configaudit && GOWORK=off go run . -root ../.. -format json) \
  > /tmp/config-audit-zero.json
jq -e '
  ((.unmigrated // []) | length) == 0 and
  ((.untracked // []) | length) == 0 and
  ((.migrated_but_still_read_outside // []) | length) == 0 and
  ((.unresolved // []) | length) == 0 and
  ((.suppressions // []) | length) == 0 and
  ((.coverage_errors // []) | length) == 0
' /tmp/config-audit-zero.json
make config-audit > /tmp/config-audit-zero.txt
test ! -s /tmp/config-audit-zero.txt
gofmt -w $(git diff --name-only --diff-filter=ACM -- '*.go')
git diff --check
git add internal/config openfeature
git add -u
git commit -m "refactor(config): finish root-module configuration migration"
```

## Task 13: Update docs and run final verification

**Files:**

- Modify: `CONTRIBUTING.md`
- Modify: `README.md`
- Modify: `internal/config/README.md`
- Modify: `scripts/configaudit/README.md`
- Modify: migration tests if final verification exposes a behavior gap

**Interfaces:**

- Consumes: all previous tasks
- Produces: release-ready branch and verification record

- [ ] **Step 1: Update documentation**

Document the blocking audit, root-module boundary, raw-read allowlist,
definition/binding split, sampling-boundary rule, generation model, product
claims, and the command used to prove a clean report.

- [ ] **Step 2: Run focused race and deadlock checks**

Run:

```sh
go test -race ./internal/config/... ./ddtrace/tracer ./profiler \
  ./ddtrace/opentelemetry/... ./openfeature ./internal/...
make test-deadlock
```

Expected: all pass.

- [ ] **Step 3: Run repository suites**

Run:

```sh
make test/unit
make test/contrib
make test/appsec
make fix-modules
git diff --check
```

Expected: all pass and module files remain clean except intentional changes.

- [ ] **Step 4: Compare tracer benchmarks**

Run the same benchmark command on the merge base and branch in separate
worktrees:

```sh
go test ./ddtrace/tracer -run '^$' \
  -bench 'Benchmark(Config|StartSpan|StartSpanConcurrent|Inject|Extract)' \
  -benchmem -count=5
```

Use `benchstat` to compare results. Fix unexplained regressions outside normal
run variance.

- [ ] **Step 5: Commit documentation or verification fixes**

Run:

```sh
git add CONTRIBUTING.md README.md internal/config/README.md scripts/configaudit/README.md
git add -u
git diff --cached --check
git commit -m "docs(config): document completed configuration migration"
```

- [ ] **Step 6: Run mandatory pre-push review**

Invoke the `pre-push-review` skill against the frozen committed snapshot. Apply
all valid findings, amend them into the commit that introduced the issue, then
rerun the affected tests and the full clean-audit proof.

- [ ] **Step 7: Record final evidence**

Save commit hashes, audit JSON counts, test commands, race results, deadlock
result, repository suite results, and benchmark comparison for the final handoff.
