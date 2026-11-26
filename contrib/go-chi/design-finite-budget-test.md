# Design: stabilize `TestFiniteBudget` in the go-chi contribs

Size: Small. Test-only change. No production code changes.

## Decisions

| # | Decision | Selected option | Reasons |
|---|----------|-----------------|---------|
| D1 | Invariant that the test must keep | With `Use(Middleware())` before routing, the middleware sends no path parameters to the WAF. The path rules do not run, so they do not spend the request budget. The response-header block rule runs and blocks (403). | This is what the code before the route report did ("main"), and what the README section "Late blocks and route reports in HTTP contribs" requires ("no use of the request budget"). The test comment "Result of 29d38928: 200 (the budget is spent)" gives the result of the regressed design, not the expected result. The comment and the assertion (403) agree. |
| D2 | Budget | One budget: `5ms`. Remove `500us` and `2ms`. | Measured WAF time of the request on the fixed code: 64-246 µs (worst case with `-race` and full CPU load). `500us` has only 2x headroom: this is the flake. `5ms` has 20x headroom. `5ms` is also in the range that other contrib tests use (`5ms`/`10ms`). |
| D3 | Keep the regression detectable at `5ms` | Use a route with 4 path parameters (`/items/{a}/{b}/{c}/{d}`), each 4096 characters, and remove `key_path` from the 5,000 path rules so that each rule reads all path parameters. | Measured cost of the path rules when they get path parameters: 1 param = 5.6-7.8 ms (too close to 5 ms); 4 params = 23-29 ms (more than 4.6x the budget). More rules (20,000) give the same cost but make the test 4x slower (ruleset build: 4-5 s with `-race`). More parameters do not change the build time. |
| D4 | Prove the regression side in the test itself | Add a control sub-test: same rules and budget, but `r.With(Middleware())`. There chi knows the path parameters when the middleware starts, the path rules run and spend the budget, and the result must be 200 with no event. | Without a control, a faster WAF or a faster CI machine can silently make the test pass on the regressed code too. The control fails if the setup no longer spends the budget. CPU load can only make the path rules slower, so load cannot make the control flaky. |
| D5 | Rejected: retry until 403 | Not selected. | A retry hides real timeouts and makes the test result depend on luck. |
| D6 | Rejected: no wall-clock budget (for example `1s`, or a mocked timer) | Not selected. | The budget in go-libddwaf is wall-clock time and is not injectable through the public API. With an unlimited budget the test no longer proves anything about the budget. Path-parameter delivery is already tested by `TestNoEarlyPathBlock`. |
| D7 | Scope | Change both copies the same way: `contrib/go-chi/chi.v5/route_only_appsec_test.go` and `contrib/go-chi/chi/route_only_appsec_test.go`. | The two files are identical except for imports. They must stay in sync. |

## Background

### How go-libddwaf enforces the budget (v5.1.0)

- `internal/appsec/listener/waf/waf.go:112` creates one WAF context per
  request with `timer.WithBudget(waf.timeout)`. `waf.timeout` comes from
  `DD_APPSEC_WAF_TIMEOUT` (`internal/appsec/config/internal_config.go:250`,
  default 2 ms).
- So the budget is **per request**. All the WAF runs of the request share it.
- `Context.Run` (go-libddwaf `context.go:170-268`):
  1. returns `ErrTimeout` at once if the context budget is spent
     (`SumExhausted`);
  2. creates a run timer with the remaining budget, and measures encode, eval
     and decode in wall-clock time;
  3. returns `ErrTimeout` after the encode if the budget is spent;
  4. gives the remaining time to the native WAF (`ContextEval`).
- dd-trace-go treats `ErrTimeout` as a normal result
  (`internal/appsec/emitter/waf/run.go:57-61`). No block action comes back,
  so the response is 200.

### Why `500us` flakes

The budget is wall-clock time. On the fixed code, the request uses 64-246 µs
of WAF time (measured on an 18-core arm64 machine, `-race`, 36 busy
processes). When the OS does not schedule the thread for some hundred
microseconds during a run (CI with parallel packages), the request goes over
500 µs before the response-header run. That run returns `ErrTimeout`, and the
response is 200.

### Measurements used for the decisions

| Case | Value |
|------|-------|
| WAF time per request, fixed code, no load | 64-138 µs (`_dd.appsec.waf.duration_ext`) |
| WAF time per request, fixed code, `-race` + full CPU load, 40 runs | min 81 µs, median 93 µs, max 246 µs |
| Path rules, 5,000 rules, 1 param x 4096 chars | 5.6-7.8 ms |
| Path rules, 20,000 rules, 1 param x 4096 chars | 25-31 ms |
| Path rules, 5,000 rules, no `key_path`, 4 params x 4096 chars | 23-29 ms |
| Proposed test, `5ms`, `-race`, full CPU load, 20 runs | `Use`: 20/20 = 403 `[response-block]`; `With`: 20/20 = 200 `[]` |
| Proposed test duration with `-race` | about 0.8 s per sub-test |

## Constraints

1. Test files only. No change to production code, to go-libddwaf, or to other
   tests.
2. Both chi copies get the same change (diff between them: imports only).
3. Use the real WAF and a real HTTP server, as the other tests in the file do.
4. No retry, no `t.Skip`, no sleep.
5. Keep the test run time near the current time (at most about 2 s per package
   with `-race`).
6. Update the test comment so that it says: the invariant, the expected
   result, the result of the regressed design, and why the budget and the
   input size were selected (with the measured numbers).

## Proposed test shape

```go
// TestFiniteBudget: 5,000 rules on all the path parameters that do not match,
// and a response header block rule. The budget is 5ms: about 20 times the WAF
// time of the request when the path rules do not run, and less than a quarter
// of the time of the path rules on 4 parameters of 4096 characters.
//   - "use": with Use, the middleware gives no path parameter. The path rules do
//     not spend the budget, and the response rule blocks (403). Result of
//     29d38928: 200 (the budget is spent).
//   - "with": control. With r.With(Middleware()), the path rules run and spend
//     the budget, so the response rule does not run (200, no event).
func TestFiniteBudget(t *testing.T) {
	const budget = "5ms"
	const route = "/items/{a}/{b}/{c}/{d}"
	// rules: "response-block" + 5,000 "match_regex" rules on roPathParams with no key_path.
	// target: "/items/" + 4 segments of 4096 characters ('a', 'b', 'c', 'd').
	t.Run("use", func(t *testing.T) {
		// roStart(t, budget, rules...); roRouter(route, &ran)
		// assert: 403, ran == true, roEvents == ["response-block"]
	})
	t.Run("with", func(t *testing.T) {
		// roStart(t, budget, rules...); r.With(chitrace.Middleware()).Get(route, handler that sets X-Stage: ready)
		// assert: 200, handler ran, roEvents is empty
	})
}
```

## Acceptance criteria

- [ ] `[unit test]` `TestFiniteBudget/use` passes in both packages: 403,
      handler ran, events = `["response-block"]`.
- [ ] `[unit test]` `TestFiniteBudget/with` passes in both packages: 200,
      handler ran, no event.
- [ ] `[unit test]` All other tests in both packages pass:
      `go test ./...` in `contrib/go-chi/chi.v5` and `contrib/go-chi/chi`.
- [ ] `[stress/race]` `go test -race -run 'TestFiniteBudget' -count=50 .` passes
      in both packages while the machine has at least 2 busy processes per CPU
      (for example `yes > /dev/null` started N times, stopped after the run).
- [ ] `[regression check]` Local only, not committed: in the `use` sub-test,
      replace `roRouter` with a router that uses `r.With(chitrace.Middleware())`.
      The sub-test must fail with 200. This proves that the `use` assertion
      detects a design that sends path parameters before the handler.
- [ ] `[review]` The diff between the two test files is imports only
      (`diff contrib/go-chi/chi.v5/route_only_appsec_test.go contrib/go-chi/chi/route_only_appsec_test.go`).
- [ ] `[review]` No change outside the two test files. No retry, skip or sleep.
- [ ] `[review]` The test comment gives the invariant, the expected results of
      both sub-tests, the result of 29d38928, and the measured margins.

## Risks

| Risk | Effect | Mitigation |
|------|--------|------------|
| CI is much slower than the measurement machine | `use` gets near 5 ms | 20x headroom over the worst measured case under load. |
| A future WAF is much faster | `with` (control) fails with 403 | The failure is the wanted signal: the test lost its discriminating power. Then increase the parameter count or the rule count. Current margin: more than 4.6x. |
| Truncation limits of the WAF change | Path rules see less data | Each parameter is 4096 characters, which is the current string limit. The control detects a change. |
| A long URL is refused | Request fails | 4 x 4096 + 10 characters is far below the 1 MB header limit of `net/http`. Measured: the requests pass. |

## Out of scope

- Production AppSec or WAF code (no bug found).
- `setups_appsec_test.go` (`1ns` is intentional) and other contribs
  (azure, envoy, haproxy use `5ms`/`10ms`).

## Verification commands

```sh
cd contrib/go-chi/chi.v5 && go test -race -run 'TestFiniteBudget' -count=50 -v .
cd contrib/go-chi/chi    && go test -race -run 'TestFiniteBudget' -count=50 -v .
cd contrib/go-chi/chi.v5 && go test -race ./...
cd contrib/go-chi/chi    && go test -race ./...
```
