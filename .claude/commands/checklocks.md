# /checklocks — Analyze and annotate lock-guarded fields and methods

Analyze a Go struct's mutex usage, propose checklocks annotations and runtime assertions, then apply them after approval.

## Arguments

`$ARGUMENTS` specifies the target in one of these formats:
- `MyType` — type in the current working directory's package
- `MyType.MyMethod` — specific method of a type
- `./path/to/package.MyType` — package-qualified type (relative path)
- `github.com/DataDog/dd-trace-go/v2/path.MyType` — full import path

If no argument is given, ask the user which struct to analyze.

## Phase 1: Parse Input

1. Parse `$ARGUMENTS` to extract:
   - **Package path**: default to CWD if unqualified. For relative paths like `./foo`, resolve to full import path.
   - **Type name**: the struct to analyze (e.g., `SpanContext`, `trace`, `Span`)
   - **Method name** (optional): if provided, scope analysis to that method only

2. Locate the source files containing the target struct definition using grep or gopls (`go_search`).

3. Verify the struct exists and contains at least one mutex field. If not, report and stop.

## Phase 2: Structural Analysis

Read all source files in the package that define or reference the target struct. Build a complete picture:

### 2a: Identify Mutex Fields

Find all fields of these types in the struct:
- `locking.Mutex` / `sync.Mutex`
- `locking.RWMutex` / `sync.RWMutex`

Record each mutex field name (e.g., `mu`).

**Naming convention:** The dd-trace-go convention is `mu` for all mutex fields. If a struct uses a different name (e.g., `m`, `lock`, `mtx`), flag it in the Phase 3 structural refactoring table (Table 5) as a rename candidate. The rename ensures consistent annotation patterns (`+checklocks:mu`) and reduces cognitive load.

**Flag embedded mutexes.** If the mutex is embedded (no field name), flag it as a design issue:
- Embedded mutexes export `Lock()`/`Unlock()` on the struct, breaking encapsulation.
- Callers can acquire/release the lock directly, bypassing any controlled access pattern.
- Checklocks must reference the type name (e.g., `+checklocks:RWMutex`) instead of a field name, making annotations fragile.
- The correct pattern is a **named unexported field** (e.g., `mu locking.RWMutex`) with lock acquisition controlled by the struct's own methods — typically unexported `*Locked` suffix helpers with `+checklocks` annotations and runtime assertions.

Report embedded mutexes in the Phase 3 plan as a **structural issue** with a recommendation to refactor.

### 2b: Identify Guarded Fields

Fields are considered guarded by a mutex if ANY of these apply:
- They appear after a `// guards below fields` comment and before the next mutex or end of struct
- They have an existing `// +checklocks:` annotation
- They are accessed inside Lock/Unlock blocks in methods of this struct
- They are accessed in `*Locked` suffix methods that assert lock ownership

When annotating a struct, if the mutex field is followed by guarded fields in a contiguous block, add or preserve a `// guards below fields` comment after the mutex field. This serves as documentation for human readers complementing the machine-readable `+checklocks:` annotations.

Preserve existing field comments. Place `// +checklocks:mu` on the line above the field when inline comments exist. Example from `rateLimiter`:
```go
mu          locking.Mutex // guards below fields
// +checklocks:mu
prevTime    time.Time     // time at which prevAllowed and prevSeen were set
// +checklocks:mu
allowed     float64       // number of spans allowed in the current period
```

Exclude from guarding:
- The mutex field itself
- Fields with `// +checkatomic` (accessed atomically)
- Fields that are only set during initialization (before the struct is shared). **Init-time access** is defined as field access that occurs:
  1. Inside a constructor/factory function (`new*`, `New*`, `init*`, `Init*`, `create*`, `Create*`, or a function that returns the struct by value), AND
  2. Before the returned struct is stored in any shared location (global variable, channel, field of an already-shared object, return from exported method on a shared receiver), AND
  3. The function itself is not called concurrently (not a method on a shared receiver, not invoked from multiple goroutines)

  **When in doubt, prefer the accessor over `+checklocksignore`.** A false-negative (missing a race) is far worse than a false-positive (unnecessary lock acquisition) for code shipped to customers.

  Note: `StartOption` functions (closures returned by `With*()`) run during `newConfig()` before the config is shared — these are init-time.

### 2b-extra: Verify Accessor Coverage

For every guarded field, check whether a lock-guarded accessor method exists (e.g., `get()` for reads, a setter for writes). If a guarded field has **no accessor but is read from outside the struct's own lock-acquiring methods**, flag it as **"missing accessor"** in the Phase 3 plan.

Adding `+checklocksignore` to every external read site is not acceptable when an accessor can be created. The rule:
- **2+ external read sites without lock** → create a read accessor
- **1 external read site** → accessor recommended, `+checklocksignore` acceptable with justification
- **0 external read sites** → no accessor needed

**Accessor types:**
- **Field accessor** (e.g., `get()`, `getOrigin()`) — returns a single guarded field. One lock acquisition per call.
- **Composite accessor** (e.g., `getCurrentAndOrigin()`, `toTelemetry()`) — returns multiple related guarded fields under a single lock. Prevents torn reads.

When a TOCTOU or torn read is detected in Phase 2d, check if a composite accessor already exists (e.g., `toTelemetry()`). If yes, use it. If no, propose one in Table 4.

### 2c: Inventory Existing Annotations

Scan for and record all existing annotations on the struct and its methods:
- `// +checklocks:fieldname` on struct fields
- `// +checklocksread:fieldname` on struct fields
- `// +checkatomic` on struct fields
- `// +checklocks:receiver.mu` on function signatures
- `// +checklocksread:receiver.mu` on function signatures
- `// +checklocksacquire:receiver.mu` on function signatures
- `// +checklocksrelease:receiver.mu` on function signatures
- `// +checklocksignore` on specific lines

### 2d: Trace Methods

For every method with the target struct as receiver, determine:

1. **Lock-acquiring methods**: Methods that call `mu.Lock()` or `mu.RLock()` at the top level, then access guarded fields. These are "public" lock wrappers. They may need `// +checklocksacquire` or no annotation (if they also unlock).

2. **`*Locked` suffix methods**: Methods whose name ends in `Locked` (e.g., `setMetaLocked`, `setBaggageItemLocked`). These expect the caller to hold the lock. They need:
   - `// +checklocks:receiver.mu` (for write lock) or `// +checklocksread:receiver.mu` (for read lock) above the `func` line
   - `assert.RWMutexLocked(&receiver.mu)` or `assert.RWMutexRLocked(&receiver.mu)` or `assert.MutexLocked(&receiver.mu)` as the first statement (if not already present)

3. **Init-time methods**: Methods/functions that set fields before the struct is shared (constructors, `new*` functions). Field accesses here need `// +checklocksignore` if the field has a checklocks annotation.

4. **Unprotected access:** Methods that access guarded fields without holding the lock and without the `*Locked` suffix. Apply this decision tree:

   ```
   Does an accessor method exist for this field?
     YES → Use it. Never suppress with +checklocksignore.
     NO  → Is this inside a constructor/init function?
             YES → +checklocksignore with "Initialization time" reason.
             NO  → Are there 2+ unguarded read sites for this field?
                     YES → Create an accessor method (flag in Phase 3).
                     NO  → Is the access provably single-threaded by design?
                             YES → +checklocksignore with invariant documented.
                             NO  → This is a real bug. Add lock acquisition.
   ```

   **Non-atomic read pairs:** When a single expression reads two guarded fields via separate lock acquisitions (e.g., `Value: dc.get(), Origin: dc.cfgOrigin`), flag it as a **torn read** — between the two reads, another goroutine could `update()` both fields, producing an inconsistent pair. Recommend using an existing composite accessor (e.g., `toTelemetry()`) or creating one.

   **TOCTOU pattern:** When a method reads a guarded field to make a decision, then reads the same or related field again to take action, the second read may see a different value if another goroutine modified the field between reads. This is true even when each individual read uses the accessor.

   Common TOCTOU shapes to detect:
   ```
   // TOCTOU: two separate lock acquisitions
   if !dc.get() {                              // Lock 1: reads current
       origin := dc.cfgOrigin                  // Lock 2: reads origin (or no lock)
       // Between Lock 1 and Lock 2, update() could change both fields
   }

   // TOCTOU: struct literal with mixed access
   cfg := telemetry.Configuration{
       Value:  dc.get(),        // Lock 1
       Origin: dc.cfgOrigin,    // No lock (or Lock 2)
   }
   ```

   **Fix:** Create a composite accessor that reads all related fields under a single lock acquisition:
   ```go
   func (dc *dynamicConfig[T]) getCurrentAndOrigin() (T, telemetry.Origin) {
       dc.mu.RLock()
       defer dc.mu.RUnlock()
       return dc.current, dc.cfgOrigin
   }
   ```
   Add composite accessors to Table 4 with status `NEW (composite)`.

5. **Closures holding locks**: Methods that acquire a lock and then access guarded fields inside a closure (anonymous function). The checklocks SSA analysis cannot track lock state across closure boundaries, producing false `return with unexpected locks held` errors. Flag these for refactoring: extract the closure body into a named method so checklocks can analyze the lock flow properly.

6. **Callback fields invoked under lock:** Functions stored as struct fields (e.g., `apply func(T) bool`) that are called from within lock-holding methods like `update()` or `reset()`. Checklocks SSA analysis cannot track lock state into callback invocations — it sees a function call to an unknown target. These require `+checklocksignore` with documentation that the callback contract guarantees the lock is held by the caller. Consider recommending extraction into a named method if the callback body accesses multiple guarded fields.

7. **Read-only after init:** Fields that are written only during initialization or under lock, where all runtime mutations go through lock-guarded methods. Reading such fields without lock outside init is technically safe IF the write path is fully controlled. However, this is fragile — future code might add an unguarded write path. Apply this decision:
   - **If an accessor exists** → always use it (zero ambiguity, minimal overhead with `RLock`)
   - **If the read is in a hot path and an accessor would cause measurable contention** → `+checklocksignore` with documentation of the write invariant AND a comment referencing which methods hold the lock during writes
   - **Default** → use the accessor. The overhead of `RLock`/`RUnlock` is negligible compared to the safety guarantee.

### 2e: Build Lock Dependency Map

Create a structured map:
```
struct → mutex_field → [guarded_fields] → [methods_accessing_each_field]
```

Determine for each guarded field:
- Which mutex guards it
- Whether read lock suffices (only read access) or write lock is required (any write access)
- Which methods access it and in what mode (read/write)

## Phase 3: Present Plan (human-in-the-loop gate)

Present FIVE tables before making any changes:

### Table 1: Struct Field Annotations

```
| Field | Annotation | Mutex | Status | Reason |
|-------|------------|-------|--------|--------|
| baggage | +checklocks:mu | mu | EXISTING | Already annotated |
| origin | +checklocks:mu | mu | NEW | Accessed in *Locked methods under mu |
| hasBaggage | +checkatomic | - | EXISTING | Atomic access only |
```

Status values: `EXISTING` (already annotated), `NEW` (to be added), `SKIP` (excluded with reason)

### Table 2: Method Annotations

```
| Method | Annotation | Status | Reason |
|--------|------------|--------|--------|
| setBaggageItemLocked | +checklocks:c.mu | NEW | *Locked suffix, writes guarded fields |
| foreachBaggageItemLocked | +checklocksread:c.mu | NEW | *Locked suffix, reads guarded fields |
| newSpanContext | (none) | SKIP | Init-time, fields not shared yet |
```

### Table 3: Runtime Assertions

```
| Method | Assertion Call | Status |
|--------|---------------|--------|
| setBaggageItemLocked | assert.RWMutexLocked(&c.mu) | EXISTING |
| foreachBaggageItemLocked | assert.RWMutexRLocked(&c.mu) | EXISTING |
| setMetaLocked | assert.RWMutexLocked(&s.mu) | EXISTING |
| setTagBoolLocked | assert.RWMutexLocked(&s.mu) | NEW |
```

### Table 4: Accessor Methods

```
| Field               | Accessor                | Status          | Read Sites | Reason                                        |
|---------------------|-------------------------|-----------------|------------|-----------------------------------------------|
| current             | get()                   | EXISTING        | 8          | Read-locks mu, returns current                |
| cfgOrigin           | getOrigin()             | NEW             | 10         | 10 read sites currently suppressed            |
| cfgOrigin           | (none)                  | SKIP            | 0 direct   | Always accessed via composite                  |
| current + cfgOrigin | getCurrentAndOrigin()   | NEW (composite) | 3          | TOCTOU: telemetry.go, remote_config.go        |
| startup             | (none)                  | SKIP            | 0 runtime  | Only accessed during init                     |
```

Status values: `EXISTING` (accessor already exists), `NEW` (to be created), `NEW (composite)` (multi-field accessor preventing torn reads), `SKIP` (not needed, with reason)

### Additional Notes

After the tables, list:
- **Structural issues** (embedded mutexes that should be refactored to named unexported fields)
- **Skipped fields** with reasons (atomic, init-only, immutable after construction)
- **Init-time ignores** needed (lines in constructors that need `// +checklocksignore`)
- **Potential bugs** found (unprotected access to guarded fields — prefer fixing with `.get()` or lock acquisition over `+checklocksignore`)
- **Torn reads** found (expressions reading multiple guarded fields via separate lock acquisitions — recommend composite accessor or single-lock block)
- **Import requirements** (whether `internal/locking/assert` import needs to be added)

### Table 5: Structural Refactoring

```
| Struct              | Change                          | Status | Reason                                               |
|---------------------|---------------------------------|--------|------------------------------------------------------|
| dynamicConfig       | Embedded RWMutex → named mu     | NEW    | Encapsulation: prevents external Lock() calls        |
| dummyTransport      | Embedded RWMutex → named mu     | NEW    | Same                                                 |
| dynInstRCState      | Embedded Mutex → named mu       | NEW    | Same                                                 |
| traceRulesSampler   | Rename m → mu                   | NEW    | Consistency: all mutexes named "mu"                  |
```

Status values: `NEW` (refactoring needed), `SKIP` (intentionally embedded, with reason)

### Wait for Approval

**Do not proceed without explicit user approval.** State: "Review the tables above. Reply 'approve' to apply changes, or provide feedback to adjust the plan."

## Phase 4: Apply Changes

After user approval, apply changes in this order:

### 4a-pre: Refactor Embedded Mutexes (if flagged in Phase 2a)

For each embedded mutex flagged as a structural issue:
1. Replace the embedded type with a named unexported field:
   ```go
   // Before:
   type myStruct struct {
       locking.RWMutex
       field T  // +checklocks:RWMutex
   }

   // After:
   type myStruct struct {
       mu    locking.RWMutex
       field T  // +checklocks:mu
   }
   ```
2. Update ALL lock call sites from `s.Lock()` → `s.mu.Lock()`, `s.RLock()` → `s.mu.RLock()`, etc.
3. Update ALL `+checklocks:` annotations from the type name to the field name (e.g., `+checklocks:RWMutex` → `+checklocks:mu`).
4. Search for external callers that use the exported `Lock()`/`Unlock()` methods — these are now compile errors and must be fixed.

**Skip this step** if the embedded mutex is intentional and external lock access is part of the struct's API contract. Document the decision in Phase 3 Additional Notes.

### 4a: Create Missing Accessor Methods

For each `NEW` entry in Table 4, add a method following the existing accessor pattern:
```go
func (dc *dynamicConfig[T]) getOrigin() telemetry.Origin {
    dc.mu.RLock()
    defer dc.mu.RUnlock()
    return dc.cfgOrigin
}
```

Then replace all runtime (non-init) reads of the guarded field with the new accessor. Remove the `+checklocksignore` annotations from those sites.

### 4b: Struct Field Annotations

Add the annotation on the line **immediately above** the field declaration. Never combine annotations with existing comments — the annotation comment must contain ONLY the annotation directive. Existing field comments must be preserved unchanged.

**CRITICAL**: The checklocks tool parses the annotation comment as a directive. If ANY extra text follows the annotation (e.g., `// +checklocks:mu some description`), the tool will fail to resolve it, treating the entire string as the field name. The annotation must be the sole content of its comment.

```go
// Before:
    prevTime time.Time // time at which prevAllowed and prevSeen were set

// CORRECT — annotation on its own line above, original comment preserved:
    // +checklocks:mu
    prevTime time.Time // time at which prevAllowed and prevSeen were set

// WRONG — annotation combined with description (checklocks cannot parse this):
    prevTime time.Time // +checklocks:mu time at which prevAllowed and prevSeen were set

// WRONG — annotation replaces the original comment (loses documentation):
    prevTime time.Time // +checklocks:mu
```

For fields without existing comments, an inline annotation is acceptable:
```go
    origin string // +checklocks:mu
```

For `+checkatomic` and `+checklocksignore`, inline is the convention:
```go
    hasBaggage uint32 // +checkatomic
```

### 4c: Method Annotations

Add `// +checklocks:receiver.mu` or `// +checklocksread:receiver.mu` on the line immediately before the `func` keyword, after any doc comments:

```go
// setBaggageItemLocked sets a baggage item.
// c.mu must be held for writing.
// +checklocks:c.mu
func (c *SpanContext) setBaggageItemLocked(key, val string) {
```

### 4d: Runtime Assertions

Add the appropriate assert call as the **first statement** in the method body (after the opening brace). Choose based on mutex type and access mode:

| Mutex Type | Access Mode | Assert Call |
|------------|-------------|-------------|
| `locking.RWMutex` | Write | `assert.RWMutexLocked(&receiver.mu)` |
| `locking.RWMutex` | Read | `assert.RWMutexRLocked(&receiver.mu)` |
| `locking.Mutex` | Any | `assert.MutexLocked(&receiver.mu)` |

Skip if the method already has the correct assert call.

### 4e: Init-Time Ignores

**`+checklocksignore` must be used sparingly.** It is ONLY acceptable in three cases:

1. **Initialization time**: `"Initialization time, not shared yet."` — Field accesses in constructors/factory functions (`new*`, `init*`) where the struct is not yet shared and no concurrent access is possible.
2. **Proven single-threaded context**: `"RC callbacks are single-threaded."` — Code paths that are demonstrably single-threaded by design (e.g., RC dispatcher serializes callbacks).
3. **Callback under lock**: `"apply is called within update/reset which already hold the lock."` — Closures/callbacks invoked from methods that hold the lock, where checklocks SSA cannot verify the lock state.

Each justification message MUST follow the format: `// +checklocksignore - <Category>. <Optional detail>.`

**Do NOT use `+checklocksignore` for:**
- Hot-path reads that skip locking for performance — use `.get()` or the appropriate lock-guarded accessor instead.
- Any access where concurrent mutation is possible, even if unlikely.
- Suppressing errors you don't understand — investigate the root cause first.

For legitimate init-time accesses, add `// +checklocksignore` inline with a reason:

```go
sc.hasBaggage = 1 // +checklocksignore - Initialization time, not shared yet.
```

### 4f: Ensure Imports

If `assert.*` calls were added and the file doesn't already import the assert package, add:
```go
"github.com/DataDog/dd-trace-go/v2/internal/locking/assert"
```

### 4g: Format

Run `gofmt` on every modified file:
```bash
gofmt -w <file>
```

## Phase 5: Verification

**Verification is the entire point of this skill.** Annotations without rigorous verification are worse than no annotations — they create false confidence. Do not cut corners. Do not skip steps. Do not run tests in the background and check only exit codes.

Run the following steps in order. Report results as a summary table.

Always run tests for the **full package**, not filtered by test name. Packages in this codebase do not have clean separation of concerns — a lock annotation change can affect any test in the package.

### Step 0: Capture Full Baseline (BEFORE any code changes)

**This step MUST happen before Phase 4.** Capture the complete state of the package on the clean (pre-change) commit so you can diff against it after applying changes. You need baselines for both static analysis and dynamic tests.

```bash
# Static baseline
./scripts/checklocks.sh ./path/to/package 2>&1 | sort > /tmp/checklocks-baseline.txt
./scripts/checklocks.sh -t ./path/to/package 2>&1 | sort > /tmp/checklocks-tests-baseline.txt

# Dynamic baselines — capture FULL output, not just exit codes
go test -count=1 ./path/to/package 2>&1 > /tmp/test-default-baseline.txt
go test -count=1 -tags=debug ./path/to/package 2>&1 > /tmp/test-debug-baseline.txt
go test -count=1 -tags=deadlock ./path/to/package 2>&1 > /tmp/test-deadlock-baseline.txt
go test -count=1 -tags=debug,deadlock ./path/to/package 2>&1 > /tmp/test-debugdeadlock-baseline.txt
```

Extract the test-level pass/fail lines for diffing:
```bash
grep -E '^--- (FAIL|PASS):' /tmp/test-default-baseline.txt | sort > /tmp/test-default-baseline-results.txt
grep -E '^--- (FAIL|PASS):' /tmp/test-debug-baseline.txt | sort > /tmp/test-debug-baseline-results.txt
grep -E '^--- (FAIL|PASS):' /tmp/test-deadlock-baseline.txt | sort > /tmp/test-deadlock-baseline-results.txt
grep -E '^--- (FAIL|PASS):' /tmp/test-debugdeadlock-baseline.txt | sort > /tmp/test-debugdeadlock-baseline-results.txt
```

Also check for assertion panics in stderr (critical for `-tags=debug`):
```bash
grep -c 'AssertRWMutexLocked failed\|AssertRWMutexRLocked failed\|AssertMutexLocked failed' /tmp/test-debug-baseline.txt > /tmp/assert-panics-baseline.txt
```

**Why all 4 configurations?** Each tests a different runtime behavior:
- **(none)**: Standard mutex, TryLock-based assertions (soft — prints but does not fail)
- **debug**: Standard mutex, TryLock-based assertions in debug mode. `assert.*` calls use `go-mutexasserts` which calls `debug.PrintStack()` on failure. These are **real runtime checks** — a `*Locked` method called without the lock WILL print a panic-like stack trace to stderr even if the test technically passes.
- **deadlock**: `go-deadlock` mutex wrappers with deadlock detection. Assertions are no-ops.
- **debug,deadlock**: `go-deadlock` wrappers. Assertions are no-ops.

### Static Verification

**Step 1: Build**
```bash
go build ./path/to/package
```

**Step 2: Vet**
```bash
go vet ./path/to/package
```

**Step 3: Checklocks (prod)**
```bash
./scripts/checklocks.sh ./path/to/package 2>&1 | sort > /tmp/checklocks-after.txt
diff /tmp/checklocks-baseline.txt /tmp/checklocks-after.txt
```

If `scripts/checklocks.sh` does not exist, fall back to:
```bash
go run gvisor.dev/gvisor/tools/checklocks/cmd/checklocks@go ./path/to/package
```

`scripts/checklocks.sh` flags:
- `-t, --include-tests` — Include test files (default: excluded via `-test=false`)
- `-i, --ignore-known-issues` — Exit 0 even if known issues found
- Positional arg: target directory (default: `./ddtrace/tracer`)
- Env: `CHECKLOCKS_BIN` — path to pre-built checklocks binary

The script filters output: lines prefixed with `-:` or `#` are treated as ignorable (checklocks tool noise). Only lines matching actual source locations count as errors.

Interpret the diff:
- Lines only in `checklocks-after.txt` = **regressions** (must fix)
- Lines only in `checklocks-baseline.txt` = **improvements** (annotation caught a pre-existing issue)
- Lines in both = **pre-existing** (out of scope, note but don't fix)
- Line number shifts on identical errors = **harmless** (added/removed lines shifted positions)

**IMPORTANT**: Do NOT filter checklocks output by modified filename. Adding annotations to a struct's fields can cause new errors in ANY file that accesses those fields — including files you did not modify. Evaluate ALL checklocks output.

**Step 3b: Checklocks with Test Files**
```bash
./scripts/checklocks.sh -t ./path/to/package 2>&1 | sort > /tmp/checklocks-tests-after.txt
diff /tmp/checklocks-tests-baseline.txt /tmp/checklocks-tests-after.txt
```

Test files that directly access guarded fields are race conditions waiting to happen. They may not trigger with `-race` today but indicate unsafe patterns that break under concurrent test execution or when CI parallelism changes.

**Action:** For each test violation:
- If an accessor exists → use it (e.g., `.get()` instead of `.current`)
- If no accessor exists → add `// +checklocksignore - Test-only access, single goroutine.` only if the test is demonstrably sequential
- Flag test violations in the summary table as a separate row

### Dynamic Verification

**CRITICAL**: Run each test configuration in the foreground, capture full output, and diff against baseline. Do NOT run tests in the background and check only exit codes or the last few lines. The `-tags=debug` build activates real mutex assertions via `go-mutexasserts` — a `*Locked` method called without the lock will print `AssertRWMutexLocked failed!` followed by a full goroutine stack trace to stderr. These assertion failures may NOT cause the test to report `FAIL` (the assert library prints but does not call `t.Fatal`), so **you must scan the full output for assertion panics, not just check exit codes**.

Run all 4 test configurations sequentially (not in background). For each one, capture full output and diff:

**Step 4: Test (default)**
```bash
go test -count=1 ./path/to/package 2>&1 > /tmp/test-default-after.txt
grep -E '^--- (FAIL|PASS):' /tmp/test-default-after.txt | sort > /tmp/test-default-after-results.txt
diff /tmp/test-default-baseline-results.txt /tmp/test-default-after-results.txt
```

**Step 5: Test (debug)** — THIS IS THE CRITICAL ONE
```bash
go test -count=1 -tags=debug ./path/to/package 2>&1 > /tmp/test-debug-after.txt
grep -E '^--- (FAIL|PASS):' /tmp/test-debug-after.txt | sort > /tmp/test-debug-after-results.txt
diff /tmp/test-debug-baseline-results.txt /tmp/test-debug-after-results.txt
```

Then check for NEW assertion panics:
```bash
grep -c 'AssertRWMutexLocked failed\|AssertRWMutexRLocked failed\|AssertMutexLocked failed' /tmp/test-debug-after.txt > /tmp/assert-panics-after.txt
diff /tmp/assert-panics-baseline.txt /tmp/assert-panics-after.txt
```

If the assertion panic count increased, your changes introduced a regression. Find the new panics:
```bash
grep -B2 -A20 'Assert.*Mutex.*failed' /tmp/test-debug-after.txt
```

**Step 6: Test (deadlock)**
```bash
go test -count=1 -tags=deadlock ./path/to/package 2>&1 > /tmp/test-deadlock-after.txt
grep -E '^--- (FAIL|PASS):' /tmp/test-deadlock-after.txt | sort > /tmp/test-deadlock-after-results.txt
diff /tmp/test-deadlock-baseline-results.txt /tmp/test-deadlock-after-results.txt
```

**Step 7: Test (debug+deadlock)**
```bash
go test -count=1 -tags=debug,deadlock ./path/to/package 2>&1 > /tmp/test-debugdeadlock-after.txt
grep -E '^--- (FAIL|PASS):' /tmp/test-debugdeadlock-after.txt | sort > /tmp/test-debugdeadlock-after-results.txt
diff /tmp/test-debugdeadlock-baseline-results.txt /tmp/test-debugdeadlock-after-results.txt
```

**Step 8: Test with race detector** (optional, run if Steps 4-7 pass)
```bash
go test -race -count=1 ./path/to/package 2>&1 > /tmp/test-race-after.txt
```

The `-race` flag adds significant overhead. Run it after the non-race configurations pass to confirm no data races were introduced.

### Verification Criteria

A step PASSES only if ALL of the following hold:
1. The diff against baseline shows **zero new failures** (new `--- FAIL:` lines not present in baseline)
2. The diff against baseline shows **zero new assertion panics** (for `-tags=debug` runs)
3. No new `panic:` or `SIGSEGV` in output that was not in baseline

A step is marked **PRE-EXISTING** if the failure exists identically in the baseline.

### Summary Table

```
| Step                        | Status       | Details                              |
|-----------------------------|--------------|--------------------------------------|
| baseline captured           | DONE         | 3 pre-existing checklocks errors     |
|                             |              | 1 pre-existing test failure          |
|                             |              | 2 pre-existing assert panics         |
| go build                    | PASS         |                                      |
| go vet                      | PASS         |                                      |
| checklocks (prod)           | PASS         | 0 new errors (3 pre-existing)        |
| checklocks (with tests)     | WARN         | 11 new test-file violations          |
| test (default)              | PASS         | 0 new failures (diff clean)          |
| test (debug)                | PASS         | 0 new failures, 0 new assert panics  |
| test (deadlock)             | PASS         | 0 new failures (diff clean)          |
| test (debug+deadlock)       | PASS         | 0 new failures (diff clean)          |
| test (race)                 | PASS         | 0 new failures (diff clean)          |
```

## Phase 6: Fix Loop (max 3 iterations)

If any verification step fails or introduces regressions vs baseline, enter a fix loop:

1. **Analyze the error**. Common patterns:
   - **Checklocks**: "field X must be accessed with lock Y held" → add missing annotation or wrap access in lock
   - **Checklocks closure errors** (`-: return with unexpected locks held`): The checklocks SSA analysis cannot track lock state across closure boundaries. It sees a lock acquired in an outer function but cannot determine whether the closure returns with the lock held. **Fix**: refactor the closure into a named method. This gives checklocks a clear function boundary to analyze and eliminates the false positive. Do not suppress these with `+checklocksignore` unless refactoring is infeasible.

   **Example — defer closure false positive:**
   ```go
   // BEFORE: checklocks reports "return with unexpected locks held"
   func (s *Span) Finish(opts ...FinishOption) {
       s.mu.Lock()
       defer s.mu.Unlock()
       defer func() {
           // This closure accesses s.taskEnd which is guarded by s.mu
           // checklocks cannot verify lock is held inside the deferred closure
           if s.taskEnd != nil {
               s.taskEnd() // +checklocksignore - Lock held by surrounding Finish()
           }
       }()
       // ...
   }
   ```

   When the closure body is trivial (1-3 lines) and the surrounding lock scope is obvious, `+checklocksignore` with documentation is acceptable. For complex closure bodies (4+ lines, multiple guarded field accesses), extract to a named `*Locked` method.

   - **New assertion panics in `-tags=debug` output** (`AssertRWMutexLocked failed!`): A `*Locked` method is being called without the lock held. The `go-mutexasserts` library prints a stack trace but does NOT call `t.Fatal()`, so the test may still report PASS. This is why you must diff assertion panic counts, not just exit codes. Find the caller in the stack trace and either fix the caller to acquire the lock, or fix the test to hold the lock before calling the `*Locked` method.
   - **Test panic from `assert.*`** → method accesses guarded field without holding lock; add lock acquisition or fix caller
   - **Data race (`-race`)** → field needs lock annotation or atomic access pattern
   - **Deadlock (`-tags=deadlock`)** → lock ordering violation; check if span.mu → trace.mu ordering is respected

### Decision Tree for Fixes

When checklocks reports "field X must be accessed with lock Y held":

```
1. Does the struct have an accessor for field X?
   → YES: Use the accessor. Remove +checklocksignore.
   → NO: Go to 2.

2. Is this inside a constructor/init function (struct not yet shared)?
   → YES: +checklocksignore with "Initialization time, not shared yet."
   → NO: Go to 3.

3. Is this inside a callback/closure called from a lock-holding method?
   → YES: +checklocksignore with "Called from update/reset which holds lock."
          Consider refactoring callback to named method.
   → NO: Go to 4.

4. Are there 2+ unguarded read sites for this field?
   → YES: Create a new accessor method. Use it at all sites.
   → NO: Go to 5.

5. Is the access provably single-threaded by documented design?
   → YES: +checklocksignore with the threading invariant documented.
   → NO: This is a real bug. Add lock acquisition or fix the design.
```

2. **Apply the fix** following the same patterns from Phase 4.

3. **Re-run verification** (Phase 5).

4. **Repeat** up to 3 times. If issues remain after 3 iterations, stop and report:
   - What was fixed
   - What remains unfixed
   - Suggested manual steps

## Reference: dd-trace-go Conventions

### Lock Encapsulation (CRITICAL)

Mutexes must ALWAYS be **named unexported fields** — never embedded:

```go
// CORRECT — named unexported field, lock is private to the struct:
type SpanContext struct {
    mu      locking.RWMutex
    baggage map[string]string // +checklocks:mu
}

// WRONG — embedded mutex, exports Lock()/Unlock() on the struct:
type dynamicConfig[T any] struct {
    locking.RWMutex
    current T // +checklocks:RWMutex  ← fragile, uses type name
}
```

Why this matters:
- Embedding exposes `Lock()`/`Unlock()` publicly, allowing any caller to acquire the lock directly.
- This bypasses controlled access patterns — there's no way to enforce that callers use the struct's own `*Locked` helpers.
- Checklocks annotations must use the type name (`RWMutex`) instead of a field name (`mu`), making them fragile to refactoring.
- The established dd-trace-go pattern uses unexported `*Locked` suffix methods annotated with `+checklocks` and guarded by runtime `assert.*` calls under `-tags=debug`.

### Naming Conventions

- `*Locked` suffix: method expects caller to hold the write lock (e.g., `setMetaLocked`, `setBaggageItemLocked`)
- Some methods use `*RLocked` to indicate read lock expectation, but the `*Locked` suffix is more common even for read-only access

### Lock Types

| Import | Type | Usage |
|--------|------|-------|
| `internal/locking` | `locking.Mutex` | Drop-in `sync.Mutex` replacement |
| `internal/locking` | `locking.RWMutex` | Drop-in `sync.RWMutex` replacement |

### Assert API

| Import | Function | Verifies |
|--------|----------|----------|
| `internal/locking/assert` | `assert.MutexLocked(&m)` | Mutex is write-locked |
| `internal/locking/assert` | `assert.RWMutexLocked(&m)` | RWMutex is write-locked |
| `internal/locking/assert` | `assert.RWMutexRLocked(&m)` | RWMutex is read-locked (or write-locked) |

### Lock Ordering

The established lock ordering in dd-trace-go is: `span.mu` → `trace.mu`. Never acquire `span.mu` while holding `trace.mu`.

### Checklocks Annotation Reference

| Annotation | Placement | Meaning |
|------------|-----------|---------|
| `// +checklocks:mu` | Line above struct field | Field requires `mu` write lock |
| `// +checklocksread:mu` | Line above struct field | Field requires `mu` read lock |
| `// +checkatomic` | Struct field (inline) | Field uses atomic access |
| `// +checklocks:r.mu` | Above `func` line | Function requires caller to hold `r.mu` write lock |
| `// +checklocksread:r.mu` | Above `func` line | Function requires caller to hold `r.mu` read lock |
| `// +checklocksacquire:r.mu` | Above `func` line | Function acquires `r.mu` |
| `// +checklocksrelease:r.mu` | Above `func` line | Function releases `r.mu` |
| `// +checklocksignore` | Inline on statement | Suppress checklocks for this line |

### Existing Annotation Examples

From `ddtrace/tracer/spancontext.go`:
```go
hasBaggage uint32 // +checkatomic
samplingDecision samplingDecision // +checkatomic
sc.hasBaggage = 1 // +checklocksignore - Initialization time, not shared yet.
sc.trace.samplingDecision = sDecision // +checklocksignore - Initialization time, not shared yet.
```

### Build Tag Matrix for Testing

| Tags | Mutex Type | Assertions |
|------|-----------|------------|
| (none) | `sync.Mutex` aliases | TryLock-based |
| `debug` | `sync.Mutex` aliases | TryLock-based (debug mode) |
| `deadlock` | `go-deadlock` wrappers | No-op (deadlock lib handles it) |
| `debug,deadlock` | `go-deadlock` wrappers | No-op (deadlock lib handles it) |

### Known Limitations

**Generics:** Checklocks annotations work with generic types (`dynamicConfig[T any]`). `// +checklocks:mu` on a field applies to all instantiations uniformly. No special handling needed.

**Multiple mutexes:** When a struct has multiple mutexes, each guarded field's annotation specifies which mutex guards it. Phase 3 tables should group fields by their guarding mutex.

**Nested struct access:** For `a.b.field` where `b` is a struct with its own mutex, the inner mutex governs `field`. Do not annotate outer struct fields with the inner mutex name. Access must go through the inner struct's accessor methods.

**Callback fields:** Functions stored as struct fields (e.g., `apply func(T) bool`) that are invoked under lock cannot be verified by checklocks SSA. Use `+checklocksignore` and document the callback contract.

**Test files:** `scripts/checklocks.sh` excludes test files by default. Run with `-t` flag to catch test-file violations. Test code that directly accesses guarded fields creates race conditions under concurrent test execution.

**Torn reads:** Checklocks analyzes individual field accesses, not multi-field consistency. Two separate lock-guarded reads in the same expression can produce inconsistent data. This must be caught manually in Phase 2d.

**Unguarded fields above the mutex:** By dd-trace-go convention, only fields listed AFTER `// guards below fields` are guarded. Fields declared before the mutex in the struct definition are intentionally unguarded (either immutable after construction, inherently thread-safe, or accessed atomically). Do not annotate these fields unless analysis reveals concurrent mutation.
