# Profiling implementation

**BEFORE making ANY code changes**, you MUST read [doc.go](./doc.go) for information about how the profiler works. 

## Key Takeaways

* Start the profiler using `profiler.Start()` and stop it with `profiler.Stop()`.
* Profiles are collected every minute and sent to the agent with runtime metrics in batches.
* The profiler supports several types of profiles (`HeapProfile`, `CPUProfile`, `BlockProfile`, `MutexProfile`, `GoroutineProfile`, and `MetricsProfile`), which are defined in [profile.go](./profile.go). 