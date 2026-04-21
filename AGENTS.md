# Project overview

## Read CONTRIBUTING.md First

**BEFORE making ANY code changes**, you MUST read [CONTRIBUTING.md](./CONTRIBUTING.md) for information about:

* Creating new PRs and commits
* Code cleanliness and style
* Testing and linting mechanisms
* Important Go conventions

Furthermore, be sure to follow [Effective Go guidelines](https://go.dev/doc/effective_go) when writing Go code.

Use the following AGENTS.md files when making specific changes:

* [contrib/AGENTS.md](./contrib/AGENTS.md) -- for updating contribs/integrations
* [ddtrace/tracer/AGENTS.md](./ddtrace/tracer/AGENTS.md) -- for updating core Datadog tracer implementations and features
* [internal/AGENTS.md](./internal/AGENTS.md) -- for updating features and implementations that are not customer facing
* [orchestrion/AGENTS.md](./orchestrion/AGENTS.md) -- for updating or creating new Orchestrion (auto-instrumentation) files
* [profiler/AGENTS.md](./profiler/AGENTS.md) -- for profiling updates

## Updating AGENTS.md

This file should be short. Only update this file if a new AGENTS.md file is added, so it must be added to the list with its purpose.

When a significant new feature is being added, add a relevant section to [CONTRIBUTING.md](./CONTRIBUTING.md).