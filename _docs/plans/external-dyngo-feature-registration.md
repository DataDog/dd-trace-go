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
4. **Independent enablement.** IAST active + AppSec inactive ⇒ the root op must
   carry IAST listeners only, and vice-versa. The current root used for
   newly-created operations exists iff at least one contributor attached
   something (old roots may still linger for in-flight operations — see
   [Swap boundary](#swap-boundary-and-in-flight-operations)).
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
// SUPERSEDED pseudocode — kept only to show the reasoning. The final API
// (see Proposed design) splits build into Prepare/Attach and requires an
// IMMUTABLE snapshot. Do not implement this shape.
RegisterWithConfig[C any](
	name string,
	provider func() (cfg C, active bool),
	build func(root Operation, cfg C) (Feature, error),
) (unregister func())
```

On each swap the runtime, per contributor, calls `provider()`; if `active` it
calls `build(root, cfg)`. Generics give type safety at the contributor boundary;
the runtime stores only type-erased closures.

**Conclusion:** the hook idea works and satisfies both AppSec and IAST, provided
config is supplied via a provider (pulled at swap time), not a value captured at
registration.

> The final API below **refines this sketch** in two ways the rest of the
> document depends on: (1) the callback is split into a fallible `Prepare` and an
> infallible `Attach`, because attaching listeners cannot be rolled back; and
> (2) the "mutate-in-place, return the same pointer" provider shown here is
> **not** safe — the snapshot must be immutable (copy-on-write). See
> [Proposed design](#proposed-design) and [Concurrency model](#concurrency-model).

## Proposed design

### New public package: `instrumentation/appsec/dyngo/runtime`

> Package name TBD (`runtime`, `feature`, `lifecycle`). It must NOT live under
> `internal/`. It builds on the already-public `dyngo.NewRootOperation` /
> `dyngo.SwapRootOperation`, keeping `dyngo` itself a pure event library and
> isolating the global registry + swap orchestration here.

#### Two-phase contributors (prepare then attach)

A naive `Builder func(root) (Feature, error)` that attaches listeners and *may
also fail* is unsafe: `dyngo.On` / `OnFinish` / `OnData` only **append**
listeners (operation.go:240-273) and there is **no per-listener unregister** —
listeners are cleared only when an operation is disabled/finished
(operation.go:225-237), which does not happen to root operations during a swap.
So a builder that attaches some listeners and then returns an error (or panics)
would leave **partial listeners** stuck on the new root, with no way to remove
them. (Today AppSec sidesteps this by never publishing a partially-built root:
it discards the new root entirely if any feature constructor errors —
features.go:55-60.)

The API therefore models each contribution as a single object with a **two-phase
lifecycle**: build it fallibly (`Prepare`), then wire it onto a root op infallibly
(`Attach`), then release it (`Stop`). One object means there is no ambiguity about
which handle to carry forward or stop.

```go
package runtime

import "github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/dyngo"

// Feature is one prepared contribution. Its lifecycle is:
//   Prepare returns it (all fallible work done) -> Attach(root) -> ... -> Stop.
type Feature interface {
	String() string
	// Attach wires this feature's listeners onto a fresh root operation. It is
	// called once per refresh that keeps this feature live (including re-attach
	// on carry-forward). It MUST be infallible (every fallible step happened in
	// Prepare) and SHOULD NOT panic. The same Feature may be Attached to several
	// successive roots over its lifetime; it must not assume a single Attach.
	Attach(root dyngo.Operation)
	// Stop releases the feature's resources. Called exactly once. Usually this is
	// after the feature is superseded by a newly-prepared Feature for the same
	// contributor (after the swap that drops it), or on unregister/teardown.
	// IMPORTANT: a freshly-prepared Feature may also be Stopped *without ever
	// being Attached* if an earlier contributor's Attach panics and aborts the
	// refresh before this Feature's attach turn. Stop must therefore release
	// whatever Prepare allocated even if Attach never ran.
	Stop()
}

// Prepare performs all fallible setup for one refresh cycle — reading the
// contributor's current config snapshot, building WAF handles, etc. — WITHOUT
// touching any root operation.
//   (nil, nil)  => contributor inactive this cycle
//   (nil, err)  => preparation failed; the contributor MUST have released any
//                  resources it allocated before returning the error
//   (f,   nil)  => ready; the runtime will call f.Attach(newRoot) on commit, OR
//                  f.Stop() (without Attach) if a later abort orphans it
type Prepare func() (Feature, error)

// Phase fixes cross-contributor dispatch order (see "Ordering and phases").
type Phase int

const (
	PhaseInfrastructure Phase = iota // span transport / trace tagging
	PhaseProduct                     // AppSec (WAF + protocols), IAST, ...
	PhaseLateObserver
)

// Registration is the per-contributor handle returned by Register. There is no
// public global Stop: lifecycle is owned per contributor.
type Registration interface {
	// Refresh re-runs every registered contributor and atomically swaps in a
	// new root operation. Returns the per-contributor outcome.
	Refresh() Result
	// Unregister removes this contributor and refreshes; its live Feature (if
	// any) is Stopped. Idempotent.
	Unregister() Result
}

// Register adds a contributor. Safe to call from init() or at runtime.
// Registering a name that already exists replaces the prior Prepare (its live
// Feature, if any, is rebuilt/superseded on the next refresh).
func Register(name string, phase Phase, p Prepare) Registration

// Result reports the outcome of a refresh.
//   - Committed is true iff the new root operation was swapped in. It is false
//     only when an Attach panic aborted the refresh, in which case the previous
//     root and all previously-live Features remain in effect unchanged. Callers
//     that need their change to have taken effect (e.g. AppSec stop/unregister)
//     must check Committed and retry on false.
//   - Errors holds per-contributor Prepare/Attach errors keyed by name (empty
//     when every contributor prepared+attached cleanly). Errors[name] is
//     populated even when carry-forward kept that contributor's previous Feature
//     live. Stop() failures are NOT reported here: Stop runs after the swap has
//     already committed, so it is telemetry/log-only and cannot change a
//     refresh's outcome.
type Result struct {
	Committed bool
	Errors    map[string]error
}

func (r Result) Err() error
```

Generic ergonomic helper (recommended public sugar), which threads a typed,
immutable config snapshot through `Prepare`:

```go
func RegisterWithConfig[C any](
	name string, phase Phase,
	snapshot func() (cfg C, active bool), // lock-free COW load (see Concurrency)
	prepare func(cfg C) (Feature, error), // fallible; must not touch root
) Registration
```

which type-erases into a `Prepare`:

```go
func() (Feature, error) {
	cfg, active := snapshot()
	if !active {
		return nil, nil
	}
	return prepare(cfg)
}
```

### Runtime internals

A package-global, mutex-guarded registry keyed by name, ordered by
`(phase, registration order)` (see [Ordering and phases](#ordering-and-phases)).
Each entry tracks its **last-good live `Feature`** (nil when inactive). `Refresh()`
runs as follows; `Feature.Stop()` calls are deliberately deferred until **after**
the lock is released so `Stop` cannot deadlock against the runtime lock:

1. Lock. `newRoot := dyngo.NewRootOperation()`.
2. **Prepare phase** — for each contributor in order (skipping entries tombstoned
   by a pending `Unregister`), call `Prepare()` (recovering panics → recorded as
   an error). Classify each: `(f,nil)` ready (`f` is a **freshly-prepared
   Feature**); `(nil,nil)` clean inactive; `(nil,err)` failed. **No root op is
   touched yet**, so a failed or inactive contributor has attached nothing.
3. **Resolve which Feature goes onto `newRoot`, per contributor.** Track two
   disjoint stop-lists, both drained *after* the swap (step 7): `supersededOld`
   (previously-live Features being replaced or dropped) and `orphanedNew`
   (freshly-prepared Features that end up not published).
   - ready `(f,nil)`: use the freshly-prepared `f`; add its previous live Feature
     (if any) to `supersededOld`.
   - failed `(nil,err)` **with** a previous live Feature: **carry forward** — keep
     the previous Feature live, record the error in `Result`. (Matches today's "a
     bad RC update keeps the previously-applied rules", per contributor.)
   - failed `(nil,err)` **without** a previous live Feature (e.g. first ever):
     inactive, error recorded.
   - clean inactive `(nil,nil)`: add the previous live Feature (if any) to
     `supersededOld`; the contributor becomes inactive.
4. **Attach phase** — for each resolved Feature in order, whether freshly-prepared
   or carried-forward, call `Attach(newRoot)` (recovering panics). `newRoot` is a
   blank slate, so carried-forward Features must be re-attached here too or they
   would be dropped on swap.
   - **Success:** continue.
   - **`Attach` panic ⇒ abort this refresh** (set `committed = false`). Discard the
     **not-yet-published** `newRoot`; the current root and every previously-live
     Feature stay live and **tracked**, and `supersededOld` is **left untouched**.
     Every **freshly-prepared** Feature from this cycle — the panic victim, any
     already attached to the doomed `newRoot`, *and* any not yet reached by the
     attach loop — is collected into `orphanedNew`. If the panicking Feature was
     instead a **carried-forward** one, it remains tracked/live on the unchanged
     current root and is added to *neither* stop-list. Record the panicking
     contributor's error, then go to step 6 (skip the swap).
5. **Swap (commit)** — `dyngo.SwapRootOperation(newRoot)` (or `nil` if nothing was
   attached); set `committed = true`. Update each entry's last-good live Feature
   and delete tombstoned entries.
6. Unlock.
7. **Stop drained Features (outside the lock)**, recovering panics so one bad
   `Stop` cannot block the others:
   - **committed:** `Stop()` `supersededOld` (replaced/dropped previous Features);
     `orphanedNew` is empty.
   - **aborted:** `Stop()` `orphanedNew` only (the freshly-prepared Features that
     will never be published — their `Prepare`-allocated resources must not leak);
     `supersededOld` is **not** stopped, because those Features are still live on
     the unchanged current root.

   Carried-forward Features are in neither list and are never stopped here.
   Recovered `Stop` panics are logged + counted in telemetry but do **not** appear
   in `Result` (the swap, if any, already happened; see `Result` semantics).
8. Return the per-contributor `Result` (including its `committed` flag).

`Unregister()` is a **staged** removal so an aborted refresh can never strand a
live Feature: it marks the entry as *tombstoned* (the next prepare phase attaches
nothing for it), runs a refresh, and **only deletes the entry and stops its
last-good Feature once the swap in that refresh commits** (step 5). If the refresh
aborts (step 4), the entry stays tombstoned-but-tracked and its Feature stays live
on the current root, and the returned `Result.Committed` is `false` so the caller
knows the removal did not take effect and can retry. (`Unregister` is idempotent:
repeated calls on an already-removed entry are no-ops returning a committed
`Result`.) If it was the last active
contributor, step 5 swaps the current root to `nil`. There is no public global
`Stop()` — a single internal/testing-only teardown may exist for the tracer's own
shutdown, but products tear down via their own `Registration`.

> **Reentrancy contract.** Snapshot accessors, `Prepare`, `Attach`, and
> `Feature.Stop()` must never call `Register` / `Refresh` / `Unregister`. (`Stop`
> runs outside the runtime lock, but a re-entrant refresh from within it is still
> a logic error.)

### How AppSec becomes a contributor

This requires an **internal refactor** of AppSec, because today's feature
constructors are one-phase: `listener.NewFeature` attaches listeners *during*
construction (e.g. waf.go:89-90, trace.go:33-34, httpsec/http.go:49-50), which
violates the "`Prepare` must not touch the root" rule. The internal
`listener.NewFeature` / `listener.Feature` must be reshaped into a prepared form,
e.g.:

```go
// internal/appsec/listener, sketch
type Prepared interface {
	Attach(root dyngo.Operation) // only dyngo.On/OnFinish/OnData here
	Stop()
	String() string
}
// constructors do allocation/handle-building only, never attach.
type NewFeature func(cfg *config.Config, build *buildState) (Prepared, error)
```

AppSec then registers **one** runtime contributor whose `Prepare` runs the
refactored `features` slice in order — preserving slice ordering, deriving
`SupportedAddresses` into local `buildState` (not a shared-config write), and on
any constructor error **stopping every already-prepared internal feature before
returning the joined error** (preserving today's all-or-nothing). On success it
returns a composite `runtime.Feature` whose `Attach` attaches every internal
feature and whose `Stop` stops them all:

```go
// internal/appsec, sketch
a.reg = runtime.RegisterWithConfig(
	"appsec", runtime.PhaseProduct, // span transport may move to PhaseInfrastructure
	func() (*config.Config, bool) { return a.cfgSnapshot.Load(), true }, // COW load
	a.prepare, // runs the refactored features slice; returns composite Feature
)
```

- AppSec `start()` → `Register(...)`, then call `Refresh()` and check the
  `Result`: a non-nil `Errors["appsec"]` **or** `Committed == false` (another
  contributor's `Attach` aborted the swap, so AppSec never went live) aborts
  startup (matching appsec.go:184-185). **On that failure AppSec must
  `Unregister()` (clear `a.reg`)** so a failed-to-start AppSec contributor is not
  left in the registry to be retried by unrelated refreshes.
- RC update → publish a new config snapshot (COW), then `a.reg.Refresh()`, and
  map `Result.Errors["appsec"]` to RC `ApplyStateError` (matching
  remoteconfig.go:171-180). Also treat `Committed == false` (another contributor
  aborted the swap) as an apply failure to report/retry, since the new rules did
  not take effect even if `Errors["appsec"]` is nil. On error the carry-forward
  rule keeps the previous good rules active.
- AppSec `stop()` → see [AppSec stop sequence](#appsec-stop-sequence).

### How IAST becomes a contributor (external module)

```go
// external IAST module, sketch
func Start() {
	cfgPtr.Store(loadIASTConfig()) // owns DD_IAST_ENABLED etc.
	reg = runtime.RegisterWithConfig(
		"iast", runtime.PhaseProduct,
		func() (*Config, bool) { c := cfgPtr.Load(); return c, c.Enabled }, // COW load
		func(cfg *Config) (runtime.Feature, error) {
			// fallible setup only; no root access. On error, release anything
			// allocated here before returning it.
			return &iastFeature{cfg: cfg}, nil
		},
	)
	reg.Refresh()
}

// iastFeature implements runtime.Feature.
func (f *iastFeature) Attach(root dyngo.Operation) {
	dyngo.OnData(root, f.onSomething) // taint listeners, etc. (infallible)
}
func (f *iastFeature) Stop()          { /* release resources */ }
func (f *iastFeature) String() string { return "iast" }
```

IAST defines its own taint-tracking operations / emitters using the already-public
`dyngo` primitives (`StartOperation`, `FinishOperation`, `EmitData`,
`ArgOf`/`ResultOf` markers). It can define typed operation wrappers by embedding
`dyngo.Operation` (the standard emitter pattern). No `dd-trace-go` change is
required for those — but note the [layering caveat](#layering-listening-vs-operation-creation-vs-emit-points):
registration only solves *listening*, not whether the operations IAST listens
for are actually created when AppSec is disabled.

## Concurrency model

Today, a single goroutine drives reconfiguration: the RC client invokes its
callbacks synchronously in its update loop (remoteconfig.go:260-262, 720-732),
and the AppSec RC handler mutates `a.cfg.WAFManager` and then calls
`SwapRootOperation()` in that same goroutine (remoteconfig.go:60, 96, 156, 171).
So the config read by the feature constructors happens-after the mutation with no
concurrent reader.

Note this is **not** protected by `a.featuresMu`: the build loop reads `a.cfg`
*before* `featuresMu` is acquired (features.go:40-41 vs 55-64). `featuresMu` only
serializes the final root swap and stop against `appsec.stop()`
(appsec.go:210-221); it does **not** guard config reads during construction.
Safety today comes from the single-writer RC goroutine, not from that mutex.

The runtime model breaks the single-writer assumption: **any** contributor can
trigger a refresh, so a refresh kicked off by IAST can run AppSec's snapshot read
+ prepare while AppSec's RC handler is mid-mutation. Several concerns follow.

### 1. Concurrent `Refresh()` calls (runtime-internal)

The runtime must serialize the entire prepare+attach+swap so snapshots and
`Prepare`/`Attach` are never invoked concurrently by the runtime. The sketch
holds `mu` across prepare/attach/swap (`Stop` runs after the lock is released).

> **Coalescing is deferred.** An earlier draft required collapsing bursts of
> `Refresh()` into one rebuild. That conflicts with the synchronous per-caller
> `Result` that AppSec start and RC error reporting need, and adds worker/dirty-
> flag complexity. v1 serializes refreshes and returns a concrete `Result` to
> each caller. Coalescing is a later optimization, added only if refresh churn is
> measured to matter.

### 2. Consistency of the config snapshot read by `Prepare`

Serializing refreshes does **not** serialize a snapshot read against the
contributor's *own* mutation — those are different code paths. A snapshot accessor
may read e.g. `a.cfg` while another goroutine mutates it. So the accessor must
return an **internally consistent snapshot**, requiring synchronization
independent of the runtime lock.

Crucially, **returning a mutable `*config.Config` and releasing a lock before
`Prepare` runs is not sufficient** — `Prepare` would still observe later in-place
mutations. AppSec's RC path performs multi-step in-place edits
(remoteconfig.go:60/74/96/156) and `WAFManager` locks individual operations, not
the whole RC transaction (wafmanager.go:87-136); `RestoreDefaultConfig` even has
a path that skips `m.mu` (wafmanager.go:151-155). A consistent snapshot therefore
means either an **immutable / deep-copied** value, or holding the contributor's
config lock across the *entire* `Prepare`.

### 3. Lock-ordering hazard (the one that bites)

The runtime calls the snapshot accessor **while holding the runtime lock** `mu`.
If it then takes the contributor's config lock, the order is `mu -> cfgLock`. A
naive reconfigure does `cfgLock -> (mutate) -> Refresh() -> mu`, i.e.
`cfgLock -> mu`. Two goroutines, opposite order, both held => deadlock.

There is a **second** lock to account for: AppSec's package-level lifecycle mutex
(`appsec.go:28-31`, held across `setActiveAppSec` -> `stop()` at
`appsec.go:146-153`). If the AppSec snapshot accessor takes that mutex under the
runtime lock, it can deadlock with AppSec stop/start, which holds the AppSec
mutex while calling unregister/refresh.

The contract must therefore require:

- **No reentrancy:** a snapshot accessor / `Prepare` / `Attach` / `Feature.Stop`
  must never call `Register` / `Refresh` / `Unregister` (would deadlock or
  recurse). `Stop` runs outside the runtime lock, but re-entering a refresh from
  it is still a logic error.
- **Do not hold any contributor lock (config lock *or* AppSec lifecycle mutex)
  across `Refresh()` / `Unregister()`:** mutate, release, *then* refresh.
- **Snapshot accessors must be cheap and non-blocking** — ideally lock-free —
  since they run inside the serialized critical section.

### Two viable shapes

**(A) Locked accessor.** The runtime serializes refreshes; the accessor holds the
contributor's config lock across the whole `Prepare` (concern 2) and never holds
that lock — nor the AppSec lifecycle mutex — when calling `Refresh`/`Unregister`
(concern 3). All of this must be documented and honored by every contributor.

**(B) Copy-on-write snapshot (recommended).** Config updates are copy-on-write:
the contributor stores config behind an `atomic.Pointer[Config]`; the mutator
builds a new immutable snapshot, `Store`s it, then refreshes. The accessor is a
lock-free atomic load:

```go
func() (*Config, bool) { c := cfgPtr.Load(); return c, c != nil }
```

It takes no lock, so the lock-ordering hazard (concern 3) disappears, the snapshot
is always consistent (concern 2), and `Prepare` sees a stable value start to end.

### AppSec migration constraints for COW

AppSec does not fit COW for free:

- **In-place mutation.** The RC path mutates `WAFManager` in place rather than
  publishing snapshots (remoteconfig.go:60-171). Adopting COW means AppSec must
  produce a new immutable config snapshot per update and publish it atomically.
- **`SupportedAddresses` is a build-time side effect.** The WAF feature *writes*
  `cfg.SupportedAddresses` during construction (waf.go:77), and the protocol
  features *read* it to decide activation (httpsec.go:35, grpcsec.go:28,
  graphqlsec.go:35, usersec.go:28, ossec/lfi.go:29). This is a mutation of shared
  config *in the middle of a build*, plus an intra-build ordering dependency
  (WAF must build before the protocol features — see
  [Ordering and phases](#ordering-and-phases)). Under COW the derived addresses
  must become **local build state** threaded from the WAF step to the dependent
  features inside AppSec's single `Prepare`, never a write back onto the shared
  snapshot.

- **`WAFManager` ownership (recommended model).** `config.Config` embeds a
  *mutable* `*WAFManager` (config.go:142) that owns a long-lived `libddwaf.Builder`
  into which RC accumulates rule config across updates (remoteconfig.go:60-156)
  and from which handles are built (`NewHandle`, used at waf.go:64). It has
  explicit `Close()` semantics (wafmanager.go:95-106). The builder is inherently
  stateful — it *is* the RC accumulator — so it cannot itself live inside an
  immutable snapshot, and (crucially) a pre-built `libddwaf.Handle` cannot either:
  a handle placed in a shared immutable snapshot would be **single-use** — adopted
  by one Feature then `Close()`d by that Feature's `Stop()`, leaving the snapshot
  pointing at a closed handle and risking a double-adopt / use-after-close if an
  unrelated refresh re-runs AppSec's `Prepare`. The decided model keeps handles
  uniquely owned per Feature:
  1. The `WAFManager`/builder stays a **single long-lived object owned by AppSec**,
     outside the COW snapshot, internally synchronized, and released only at AppSec
     stop (the existing `WAFManager.Reset()`/`Close()` call at appsec.go:217).
  2. RC mutations to the builder must be made **transactional** — one critical
     section around the whole multi-step update (today's per-op locking at
     wafmanager.go:87-136 is not enough; `RestoreDefaultConfig` even skips `mu`
     at wafmanager.go:151-155) — paired with a monotonically increasing **rules
     generation**. RC mutates, bumps the generation, publishes an **immutable**
     snapshot carrying the scalar config (timeouts, rate limits, APISec) + that
     generation (**no handle, no builder**), then refreshes.
  3. Each AppSec `Prepare` builds a **fresh** handle via `NewHandle` (waf.go:64)
     under the builder's transaction lock — so it always sees a consistent,
     fully-applied rule set, never a half-update. That handle is **uniquely owned**
     by the `runtime.Feature` this `Prepare` produces.
  4. The Feature `Close()`s its own handle in `Stop()`. Because handles are
     per-Feature (never shared via the snapshot), the runtime's
     `supersededOld` / `orphanedNew` stop-lists cleanly cover every case — replaced,
     carried-over-then-replaced, orphaned-on-abort, and unregistered — with no
     dangling reference left in any published snapshot.
  5. **Cost / optimization (deferred, needs a new runtime signal).** This rebuilds
     the AppSec handle on *every* refresh, including ones other contributors
     trigger while AppSec's rules are unchanged. If `NewHandle` proves too costly,
     `Prepare` could signal "unchanged — keep my current Feature, no rebuild". Note
     this is **distinct** from the runtime's existing carry-forward, which is
     triggered by a `Prepare` *error*: this is a deliberate, non-error "unchanged"
     outcome and would need its own signal (e.g. a `PrepareResult{Unchanged:true}`
     sentinel, *not* recorded in `Result.Errors`). Optimization only, not required
     for correctness.

Adopting COW removes a whole class of concurrency bugs and is the model
recommended for any new contributor (IAST) from the start. **The registration
mechanism itself does not depend on any of this** — IAST's config is simple and
fits COW trivially. The `WAFManager` transactionality and snapshot refactor are a
constraint on the *AppSec migration step* (migration step 2), not on the public
`runtime` package, and can land independently.

## Swap boundary and in-flight operations

`SwapRootOperation` affects only **future** operations: `NewOperation(nil)`
captures the current root at creation time (operation.go:138-142). It
deliberately does **not** finish the old root, and "concurrent uses of the old
root on already existing and running operations are still valid"
(operation.go:75-83).

Consequence: after a refresh or unregister, operations already in flight against
the previous root keep firing the previous generation's listeners. If the runtime
then `Stop()`s a superseded feature that releases resources — e.g. the WAF feature
`Stop()` closes the libddwaf handle (waf.go:169-170) — an in-flight operation
could call into a released resource. This hazard exists **today**: AppSec carries
an explicit TODO to block until no requests use dyngo operations
(appsec.go:219) and currently accepts best-effort semantics.

Decision for this design:

- **v1 (recommended): keep today's best-effort semantics.** Document that
  `Feature.Stop()` may run while in-flight operations still reference the
  superseded feature, and require `Stop()` implementations to tolerate concurrent
  in-flight use rather than hard-releasing resources that listeners may still
  touch.
- **Future hardening:** defer `Stop()` / resource release until the old root
  quiesces. dyngo has no operation refcounting today, so this is a separate,
  larger change and is explicitly out of scope for v1.

## Ordering and phases

dyngo dispatches listeners in append order (registration order; operation.go:353
adds, 379-380 dispatches), so the order contributors attach listeners is the order
those listeners run.

Two intra-AppSec orderings must be preserved, and both stay internal to AppSec's
single `Prepare`/`Attach`:

- `trace.NewAppsecSpanTransport` must run first — it registers start listeners
  that later attach the data listeners products rely on (features.go:24-33,
  trace.go:30-46).
- The WAF feature must build before the protocol features because of the
  `SupportedAddresses` dependency (see Concurrency).

Cross-contributor ordering needs an explicit, stable contract — raw global
insertion order is too implicit (it would depend on whether IAST or AppSec
registered first). Proposed phases, in dispatch order:

1. **Infrastructure** — span transport / trace tagging.
2. **Product listeners** — AppSec (WAF + protocols), IAST.
3. **Late observers** — optional.

> **Open decision:** whether to split the span transport out of the AppSec
> contributor into an always-on infrastructure contributor, so its "first"
> guarantee and span tagging hold even when AppSec is inactive but IAST is active.

## AppSec stop sequence

The migration sketch's "`Unregister()` + refresh" omits cleanup AppSec does today
(appsec.go:206-221). The full sequence:

1. Mark AppSec inactive safely (publish an inactive snapshot / clear `started`).
2. Disable RC blocking / capabilities **first** (appsec.go:207-208) so later
   steps are not concurrent with RC-driven refreshes.
3. `a.reg.Unregister()` → refresh; if AppSec was the last active contributor the
   current root swaps to `nil`. (Must **not** be called while holding the AppSec
   lifecycle mutex — see Concurrency, concern 3.) **Check `Result.Committed`:** if
   another contributor's `Attach` panicked, the refresh aborted, AppSec's removal
   did **not** take effect, and its Feature (and WAF handle) is still live on the
   current root. AppSec must **not** proceed to step 4 in that case — retry the
   `Unregister` (or escalate) until it commits, otherwise step 4 would release the
   manager out from under a still-live AppSec Feature.
4. Once the unregister committed, reset/release the WAF manager (the
   `WAFManager.Reset()` call at appsec.go:217; switch to `Close()` if full builder
   release is intended). Note `Feature.Stop()` only closes the *per-Feature WAF
   handle* (waf.go:169-170); releasing the long-lived manager/builder stays
   AppSec's responsibility, not the runtime's.
5. Emit stop telemetry (appsec.go:206).

## Telemetry

"log + telemetry" must be made concrete. Split responsibilities:

- **Runtime-level, product-agnostic, tagged by contributor name** (e.g.
  `contributor:appsec` / `contributor:iast`): refresh attempt/success/failure,
  per-contributor active/inactive, `Prepare`/`Attach`/`Stop` error and panic
  counts, carry-forward events, refresh duration, active-contributor count.
- **Product-level:** AppSec keeps its existing start/stop/start-error telemetry
  (appsec.go:65, 131-133, 206) inside its contributor; IAST owns its own.

## Layering: listening vs operation creation vs emit points

Registration solves only listener **(re)attachment**. Two further layers are
required end-to-end and are **not** addressed by this design:

- **Top-level operation / context creation.** The WAF context and service-entry
  span operation are created by AppSec emitters (waf/context.go:71-76,
  httpsec/http.go:94-97), and many contribs gate AppSec code paths on
  `appsec.Enabled()` (chi.go:65-66, echo.v5/echotrace.go:145-146,
  database/sql/conn.go:62-65). So with AppSec disabled those operations may never
  be created, leaving IAST listeners nothing to observe.
- **IAST source/sink emit points.** Taint sources/sinks must be emitted from
  instrumented libraries (contribs / Orchestrion).

For the "IAST active + AppSec inactive" scenario the design must state which
operations are still emitted and which require new instrumentation. **This is the
real gating dependency for a working external IAST**; registration is necessary
but far from sufficient.

## Behavior changes

- **Fault isolation across products (now safe).** Today all feature constructors
  run; if any returns an error the *new* root is discarded, the newly-built
  features are stopped, the *old* root/features stay active, and the swap returns
  an error (features.go:40-60). The runtime isolates failures per **contributor**
  via the two-phase prepare/attach split: a contributor that fails to `Prepare`
  never attaches listeners (no partial-listener pollution — the original reason a
  one-phase builder was unsafe), and a previously-live contributor is **carried
  forward** on its last-good `Feature` so a bad reconfigure does not drop its
  protection. AppSec keeps its internal join-then-fail semantics among its own
  features.
- **Per-contributor `Result` replaces the single error return.** AppSec start
  reads `Result.Errors["appsec"]` to decide whether to abort
  (appsec.go:184-185); RC maps it to `ApplyStateError` (remoteconfig.go:171-180).
- **No public global `Stop()`.** Lifecycle is per `Registration`; a global
  teardown, if any, is internal/testing-only.
- **Root op lifetime decoupled from AppSec.** The current root used for
  *newly-created* operations is absent (i.e. `NewOperation` gets no parent) iff no
  contributor attached anything — not only when AppSec started. Old root
  operations may still exist for in-flight work (see Swap boundary). AppSec's
  WAF/libddwaf gate stays inside the AppSec contributor; it no longer gates the
  runtime.

## Open questions / risks

1. **Package placement & name.** `instrumentation/appsec/dyngo/runtime` vs a
   sibling package vs folding into `dyngo`. Folding into `dyngo` keeps all global
   root state in one place but muddies dyngo's "pure event lib" role.
2. **Cross-contributor ordering contract.** See
   [Ordering and phases](#ordering-and-phases). Open decision: keep the span
   transport inside the AppSec contributor, or split it into an always-on
   infrastructure contributor so its ordering/tagging holds when AppSec is
   inactive but IAST is active.
3. **Refresh churn / concurrency.** Any contributor's reconfigure re-instantiates
   every contributor under one lock. See [Concurrency model](#concurrency-model)
   for the locking contract and the locked-accessor vs copy-on-write trade-off
   (COW recommended). Coalescing is deferred (synchronous `Result` is needed first).
4. **Carry-forward bookkeeping.** Keeping a failed contributor's last-good `Feature`
   alive (so a bad reconfigure doesn't drop protection) requires the runtime to
   retain each contributor's last-good live `Feature` (re-`Attach`ed to the new
   root on carry-forward), and to *not* stop
   carried-forward features. Edge cases to pin down: carry-forward across an
   `Unregister` of a *different* contributor; first-ever prepare failure (no
   last-good); and bounding how long a stale carried-forward `Feature` may persist.
5. **Who starts IAST.** `appsec.Start` is invoked by tracer startup. The external
   IAST module needs its own start trigger (its own `Start`, an `init`, or a
   tracer hook). Out of scope for the registry, required end-to-end.
6. **`Operation` external-implementability.** External code cannot implement
   `dyngo.Operation` from scratch (the `unwrap()` method is unexported,
   operation.go:40-45), but it **can** define typed operation wrappers by
   *embedding* `dyngo.Operation` — the standard emitter pattern (e.g.
   sqlsec/sql.go:19-21). That is sufficient for normal emitters; only exotic
   from-scratch operation implementations are unsupported.
7. **Operation creation, not just listening.** See
   [Layering](#layering-listening-vs-operation-creation-vs-emit-points). This is
   the real gating dependency for external IAST: registration reattaches
   listeners, but contribs gate operation/context creation on `appsec.Enabled()`,
   so IAST-active/AppSec-inactive may produce nothing to listen to without
   additional instrumentation work.

## Migration steps (incremental)

1. Add the public `runtime` package: two-phase `Prepare`/`Attach`, `Phase`-ordered
   `Registration` handle, `Result`, generic `RegisterWithConfig`, panic recovery
   (prepare/attach/stop), and carry-forward. Unit tests: re-attachment across
   refresh; prepare-failure isolation (asserts **no** partial listeners on the new
   root); carry-forward keeps the last-good `Feature` on a failed re-prepare;
   attach-panic aborts the refresh and keeps the old root; current root absent
   when no contributor attaches; `Unregister`/restart; `Stop` panic isolation.
2. Refactor `internal/appsec` into a single "appsec" contributor: publish COW
   config snapshots, thread `SupportedAddresses` as local build state (not a
   shared-config write), route start / RC-update / stop through the `Registration`
   handle, and map `Result.Errors["appsec"]` to startup abort / RC `ApplyState`.
   Verify existing AppSec tests pass unchanged.
3. Wire runtime + product telemetry (see [Telemetry](#telemetry)).
4. Document the public extension mechanism (CONTRIBUTING.md per repo AGENTS.md).
5. Prototype an external IAST contributor (separate module): register, refresh,
   validate it runs with AppSec disabled — and explicitly assess the
   operation-creation layering gap (which contribs emit operations without
   AppSec).
6. (Separate workstream) Define IAST emitters/operations and wire emit points in
   contribs/Orchestrion.
```
