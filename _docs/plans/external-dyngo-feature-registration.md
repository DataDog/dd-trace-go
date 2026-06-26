# Design: External dyngo feature registration (separate runtime)

Status: Draft
Author: (to fill in)
Date: 2026-06-26

## Goal

Allow code living **outside** the `dd-trace-go` module — e.g. a standalone IAST
module — to contribute dyngo emitters and listeners that participate in the same
operation tree as AppSec, with a lifecycle independent of AppSec / Threats
Protection.

Concretely, an external module must be able to:

1. Register listeners that survive the periodic root-operation swaps.
2. Run even when Threats Protection (the WAF) is disabled or `libddwaf` is
   unusable on the platform.
3. Own and (re)load its **own** configuration object, on its own schedule,
   without coupling to AppSec's `*config.Config`.

## Background: how dyngo / AppSec works today

### One global root operation

`dyngo` keeps a single process-wide root operation in an atomic pointer:

```go
// instrumentation/appsec/dyngo/operation.go
var rootOperation atomic.Pointer[Operation]
```

`NewOperation(nil)` resolves its parent from that pointer, so **every** operation
created by **any** emitter ultimately chains to the one global root. Events
bubble up the parent chain, so all listeners registered on the root see every
operation in the process.

```go
func NewOperation(parent Operation) Operation {
	if parent == nil {
		if ptr := rootOperation.Load(); ptr != nil {
			parent = *ptr
		}
	}
	return &operation{parent: parent}
}
```

The only way to install or replace the set of listeners is to build a fresh root
operation, attach listeners to it, and atomically swap it in:

```go
func SwapRootOperation(newOp Operation) { rootOperation.Swap(&newOp) }
```

### The feature-constructor pattern (internal)

AppSec owns swap orchestration. Its features are a static, **internal** slice of
constructors:

```go
// internal/appsec/features.go
var features = []listener.NewFeature{
	trace.NewAppsecSpanTransport, // must be first
	waf.NewWAFFeature,
	httpsec.NewHTTPSecFeature,
	// ...
}

// internal/appsec/listener/feature.go
type Feature interface {
	String() string
	Stop()
}
type NewFeature func(*config.Config, dyngo.Operation) (Feature, error)
```

On every (re)configuration, `appsec.SwapRootOperation` builds a fresh root, calls
every constructor with that root + the current `*config.Config`, collects the
returned `Feature`s, atomically swaps, then `Stop()`s the old features.

This loop runs at:

- **start** — `appsec.start()` → `SwapRootOperation()`
- **every remote-config update** — `remoteconfig.go:171` mutates `a.cfg` (e.g.
  WAF rules) then calls `SwapRootOperation()`
- **stop** — `dyngo.SwapRootOperation(nil)` + `Stop()` all features

### Why external code cannot participate today

| Component | Path | Visibility |
| --- | --- | --- |
| `dyngo` core (`On`, `OnFinish`, `OnData`, `EmitData`, `Start/FinishOperation`, `SwapRootOperation`, `NewRootOperation`) | `instrumentation/appsec/dyngo` | public |
| Emitters (httpsec, grpcsec, sqlsec, ossec, …) | `instrumentation/appsec/emitter/*` | public |
| `listener.Feature` / `listener.NewFeature` | `internal/appsec/listener` | internal |
| Feature registry (`var features`) | `internal/appsec/features.go` | internal |
| `config.Config` (handed to every constructor) | `internal/appsec/config` | internal |
| Swap orchestration & lifecycle | `internal/appsec` | internal |

`dyngo` itself is importable, but:

- There is no public, durable handle to "the current root operation".
- Even calling `dyngo.On(root, …)` directly is futile: the next `SwapRootOperation`
  (start / RC update / stop) silently discards listeners bound to the old root.
- The re-binding mechanism (the constructor loop) and the config object it
  depends on are both internal.

So the thing to expose is **not** "access to the root op" — it is a public way to
**participate in the re-instantiation loop**, with the contributor owning its own
config.

## Constraints / requirements the design must satisfy

1. **Survives swaps.** A contributor's listeners must be re-attached on every new
   root operation.
2. **Contributor-owned config, type-safe.** AppSec keeps using `*config.Config`;
   IAST defines and owns a different config type. The runtime must not know about
   either concrete type.
3. **Dynamic reconfiguration.** RC mutates AppSec config and re-swaps. Any
   contributor must be able to trigger a re-swap when *its* config changes, and
   the re-swap must pick up the **latest** config of **every** contributor (since
   they all share one root).
4. **Independent enablement.** IAST active + AppSec inactive ⇒ root op must exist
   with IAST listeners only, and vice-versa. The root op is non-nil iff at least
   one contributor is active.
5. **Fault isolation.** A failure while (re)building one contributor must not tear
   down the others. (This is a deliberate change from today's "one feature error
   aborts the whole swap" — see [Behavior changes](#behavior-changes).)
6. **External-importable.** Everything a contributor needs must live outside
   `internal/`.

## Evaluating the proposed `OnNewRootOperation[ConfigType]` hook

The sketched signature was:

```go
OnNewRootOperation[ConfigType any](cb func(op Operation, cfg ConfigType))
```

This captures the right *shape* — a callback invoked with the new root op and a
contributor-typed config — but as written it cannot satisfy requirement 3:
**where does the `ConfigType` value come from when the runtime invokes `cb` at
swap time?** If the value is supplied once at registration, it goes stale on the
next RC-driven re-swap.

The fix is to register a **config provider** alongside the callback, so the
runtime fetches the current config at each swap:

```go
RegisterWithConfig[C any](
	name string,
	provider func() (cfg C, active bool),
	build func(root Operation, cfg C) (Feature, error),
) (unregister func())
```

On each swap the runtime, per contributor, calls `provider()`; if `active` it
calls `build(root, cfg)`. Generics give type safety at the contributor boundary;
the runtime stores only type-erased closures. The provider model handles both
mutate-in-place (AppSec mutates `a.cfg`, provider returns the same pointer) and
full-replacement config updates.

**Conclusion:** the hook idea works and satisfies both AppSec and IAST, provided
config is supplied via a provider (pulled at swap time), not a value captured at
registration.

## Proposed design

### New public package: `instrumentation/appsec/dyngo/runtime`

> Package name TBD (`runtime`, `feature`, `lifecycle`). It must NOT live under
> `internal/`. It builds on the already-public `dyngo.NewRootOperation` /
> `dyngo.SwapRootOperation`, keeping `dyngo` itself a pure event library and
> isolating the global registry + swap orchestration here.

Core (non-generic) API — everything else is sugar over this:

```go
package runtime

import "github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/dyngo"

// Feature is a live contribution to the current root operation.
type Feature interface {
	String() string
	Stop()
}

// Builder attaches a contributor's listeners to the given (fresh) root
// operation, reading the contributor's own current configuration.
//   - returns (nil, nil)  => contributor inactive for this cycle
//   - returns (nil, err)  => build failed; isolated to this contributor
//   - returns (f, nil)    => active; f.Stop() called when superseded/stopped
type Builder func(root dyngo.Operation) (Feature, error)

// Register adds a contributor. Safe to call from init() or at runtime.
// The returned function removes the contributor and triggers a Refresh.
func Register(name string, b Builder) (unregister func())

// Refresh rebuilds the root operation from all registered builders and
// atomically swaps it in, then stops the superseded features. Any contributor
// may call this when its configuration changed.
func Refresh()

// Stop swaps the root operation to nil and stops all live features. The
// registry is retained so a later Refresh can bring contributors back.
func Stop()
```

Generic ergonomic helper (recommended public sugar):

```go
func RegisterWithConfig[C any](
	name string,
	provider func() (cfg C, active bool),
	build func(root dyngo.Operation, cfg C) (Feature, error),
) (unregister func())
```

which wraps into a `Builder`:

```go
func(root dyngo.Operation) (Feature, error) {
	cfg, active := provider()
	if !active {
		return nil, nil
	}
	return build(root, cfg)
}
```

### Runtime internals

- A package-global, mutex-guarded registry: `map[name] -> Builder` (plus
  insertion order for deterministic listener registration order).
- `Refresh()` serializes on the mutex and:
  1. `newRoot := dyngo.NewRootOperation()`
  2. for each builder in order: call it; on error, log + telemetry and skip
     (fault isolation); collect non-nil features.
  3. `dyngo.SwapRootOperation(newRoot)` (or `nil` if zero active features).
  4. `Stop()` the previously-live features.
- `Stop()` swaps to `nil` and stops all live features.

### How AppSec becomes a contributor

AppSec stops calling `dyngo.SwapRootOperation` / `dyngo.NewRootOperation`
directly. Instead it registers a **single** runtime contributor whose `build`
runs its existing internal `features` slice against the supplied root, preserving
its current ordering and its internal error-join semantics **within** the AppSec
contributor:

```go
// internal/appsec, sketch
unregister := runtime.RegisterWithConfig(
	"appsec",
	func() (*config.Config, bool) { return a.cfg, a.enabled() },
	func(root dyngo.Operation, cfg *config.Config) (runtime.Feature, error) {
		// existing loop over the internal `features` slice, attaching to root,
		// returning a composite Feature whose Stop() stops them all.
	},
)
```

- AppSec `start()` → `Register(...)` then `runtime.Refresh()`.
- RC update → mutate `a.cfg` as today, then `runtime.Refresh()`.
- AppSec `stop()` → `unregister()` (which refreshes; if it was the last active
  contributor the root op becomes nil).

### How IAST becomes a contributor (external module)

```go
// external IAST module, sketch
func Start() {
	cfg := loadIASTConfig() // owns DD_IAST_ENABLED etc.
	unregister = runtime.RegisterWithConfig(
		"iast",
		func() (*Config, bool) { return cfg, cfg.Enabled },
		func(root dyngo.Operation, cfg *Config) (runtime.Feature, error) {
			f := &iastFeature{cfg: cfg}
			dyngo.OnData(root, f.onSomething) // taint listeners, etc.
			return f, nil
		},
	)
	runtime.Refresh()
}
```

IAST defines its own taint-tracking operations / emitters using the already-public
`dyngo` primitives (`StartOperation`, `FinishOperation`, `EmitData`,
`ArgOf`/`ResultOf` markers). No `dd-trace-go` change is required for those.

## Concurrency model

Today there is effectively a single writer: the RC handler goroutine mutates
`a.cfg` and then calls `SwapRootOperation()` **synchronously in the same
goroutine**, so the config read in the build loop always happens-after the write
with no concurrent reader. (Concurrent swaps between RC update and stop are
serialized by `a.featuresMu`.)

The runtime model breaks that coupling: **any** contributor can trigger a
refresh, so a refresh kicked off by IAST can run AppSec's provider while AppSec's
RC handler is mid-mutation. Three concerns follow.

### 1. Concurrent `Refresh()` calls (runtime-internal)

The runtime must serialize the entire rebuild+swap so providers and builders are
never invoked concurrently by the runtime. The sketch holds `mu` across
`refresh()`. Refreshes must also **coalesce**: a burst of `Refresh()` calls from
different contributors (e.g. at startup) should collapse into a single rebuild
(dirty-flag, or a single serialized refresh worker), otherwise N contributors
triggering means N full rebuilds.

### 2. Consistency of the config the provider reads

Serializing refreshes does **not** serialize a provider's read against the
contributor's own mutation — those are different code paths. A provider may read
e.g. `a.cfg` while another goroutine mutates it. Therefore the provider must
return an **internally consistent snapshot**, which means the config object needs
its own synchronization independent of the runtime lock.

### 3. Lock-ordering hazard (the one that bites)

The runtime calls the provider **while holding the runtime lock** `mu`. If the
provider then takes the contributor's config lock, the lock order is
`mu -> cfgLock`. A naive contributor reconfigure does
`cfgLock -> (mutate) -> Refresh() -> mu`, i.e. `cfgLock -> mu`. Two goroutines,
opposite order, both held => classic deadlock.

The contract must therefore require:

- **No reentrancy:** a provider/builder must never call `Register` / `Refresh` /
  `Stop` (would deadlock or recurse).
- **Do not hold your config lock across `Refresh()`:** mutate config (acquire +
  release your own lock), *then* call `Refresh()`. As long as the mutate path
  releases `cfgLock` before acquiring `mu`, there is no inversion — the provider
  can safely take `cfgLock` under `mu`.
- **Providers must be cheap and non-blocking:** they run inside the serialized
  critical section and gate every other contributor's refresh.

### Two viable shapes

**(A) Locked provider.** The runtime serializes refreshes; providers take their
own config lock; the no-reentrancy and don't-hold-lock-across-`Refresh` rules
above are documented and must be honored by every contributor.

**(B) Copy-on-write snapshot provider (recommended).** Make config updates
copy-on-write: the contributor stores its config behind an
`atomic.Pointer[Config]`; the mutator builds a new immutable snapshot, `Store`s
it, then triggers `Refresh()`. The provider becomes a lock-free atomic load:

```go
func() (*Config, bool) { c := cfgPtr.Load(); return c, c != nil }
```

This takes no lock, so the lock-ordering hazard (concern 3) disappears entirely,
the snapshot is always consistent (concern 2), and coalescing is trivially
correct because every rebuild reads the latest published snapshot.

**Trade-off:** AppSec today mutates `WAFManager` in place (`RestoreDefaultConfig`
and in-place rule swaps in `remoteconfig.go`), not via immutable snapshots.
Adopting COW requires refactoring AppSec's RC path to publish new snapshots
instead of mutating in place. That is more work, but removes a whole class of
concurrency bugs and is the model recommended for any new contributor (IAST)
from the start.

## Behavior changes

- **Fault isolation across products.** Today one feature constructor returning an
  error aborts the entire swap and stops all newly-built features
  (`errors.Join` in `features.go`). In the runtime model, errors are isolated per
  **contributor**: AppSec retains its internal join-and-abort among its own
  features, but an IAST build failure must not disable AppSec, nor vice-versa.
- **Root op lifetime decoupled from AppSec.** The root op is now non-nil whenever
  any contributor is active, not only when AppSec started. AppSec's WAF/libddwaf
  gate stays inside the AppSec contributor; it no longer gates the runtime.

## Open questions / risks

1. **Package placement & name.** `instrumentation/appsec/dyngo/runtime` vs a
   sibling package vs folding into `dyngo`. Folding into `dyngo` keeps all global
   root state in one place but muddies dyngo's "pure event lib" role.
2. **Listener ordering across contributors.** Within AppSec,
   `trace.NewAppsecSpanTransport` must run first. Ordering *between* AppSec and
   IAST listeners on the shared root needs a defined, stable contract (registry
   insertion order is the proposed default; revisit if a product needs priority).
3. **Refresh churn / concurrency.** AppSec RC refreshes and IAST refreshes both
   rebuild the whole world under one lock. Acceptable today (infrequent), but note
   the coupling: any contributor's reconfigure re-instantiates every contributor.
   See [Concurrency model](#concurrency-model) for the locking contract and the
   locked-provider vs copy-on-write trade-off (the latter is recommended).
4. **Who starts IAST.** AppSec.Start is invoked by tracer startup. The external
   IAST module needs its own start trigger (its own `Start`, an `init`, or a
   tracer hook). Out of scope for the registry itself but required end-to-end.
5. **`Operation` is not externally implementable.** Its `unwrap()` method is
   unexported, so external code can only *consume* operations (receive the root,
   attach listeners, create child operations) — never implement the interface.
   This is fine for builders but constrains exotic external emitters.
6. **IAST needs instrumentation points, not just registration.** Taint
   sources/sinks must be emitted from instrumented libraries (contribs /
   Orchestrion). This design only unblocks *listening*; the emit side across
   contribs is a separate, larger workstream and is the real gating dependency
   for a working IAST.

## Migration steps (incremental)

1. Add the public `runtime` package (registry + `Refresh` + `Stop` + generic
   helper) with unit tests covering: re-attachment across refresh, contributor
   fault isolation, root-op nil when no active contributors, stop/restart.
2. Refactor `internal/appsec` to register a single "appsec" contributor and route
   start / RC-update / stop through `runtime.Refresh` / `unregister` instead of
   calling `dyngo.SwapRootOperation` directly. Verify existing AppSec tests pass
   unchanged.
3. Document the public extension mechanism (CONTRIBUTING.md per repo AGENTS.md).
4. Prototype an external IAST contributor (separate module) that registers a
   builder and refreshes; validate it runs with AppSec disabled.
5. (Separate workstream) Define IAST emitters/operations and wire emit points in
   contribs/Orchestrion.
```
