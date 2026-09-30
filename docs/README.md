# docs/

Reference material that's linked from [CONTRIBUTING.md](../CONTRIBUTING.md) but not required reading for every change. 

* [ci-workflows.md](./ci-workflows.md) -- what each CI workflow checks, which workflows run on a given diff, CODEOWNERS pattern rules, benchmark gating, and how to reproduce checks locally.
* [environment-variables.md](./environment-variables.md) -- adding a new `DD_*` environment variable, the configinverter workflow, and the registry/CI failures that come up around it.
* [debugging.md](./debugging.md) -- diagnosing goroutine leaks, including ones that only reproduce in CI.
