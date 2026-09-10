# Project overview

## Read CONTRIBUTING.md First

**BEFORE writing or editing ANY code**, you MUST read [CONTRIBUTING.md](./CONTRIBUTING.md) and [README.md](./README.md).

Furthermore, be sure to follow [Effective Go guidelines](https://go.dev/doc/effective_go) when writing Go code.

Use the following AGENTS.md files when making specific changes:

* [contrib/AGENTS.md](./contrib/AGENTS.md) -- for updating contribs/integrations
* [ddtrace/tracer/AGENTS.md](./ddtrace/tracer/AGENTS.md) -- for updating core Datadog tracer implementations and features
* [internal/AGENTS.md](./internal/AGENTS.md) -- for updating features and implementations that are not customer facing
* [orchestrion/AGENTS.md](./orchestrion/AGENTS.md) -- for updating or creating new Orchestrion (auto-instrumentation) files
* [profiler/AGENTS.md](./profiler/AGENTS.md) -- for profiling updates

## Updating Documentation

This AGENTS.md should be short. Only update this file if a new AGENTS.md file is added, so it must be added to the list with its purpose.

The developer should update [CONTRIBUTING.md](./CONTRIBUTING.md) with new, significant features. A feature may be considered significant when:

1. It introduces a new method of interacting with and/or customizing the tracer (ie new scripts for generating files, options for configuration sources, etc)
2. A new internal functionality is introduced that can replace a common, built-in Go library
3. The `make` command supports a new flag that introduces new testing/linting/building functionality AND/OR
4. A new CI workflow in GitHub or GitLab is created

The developer should also update [README.md](./README.md) with new options to the `make` command and other important commands that are essential for testing or building the tracer.

If these updates are not made, tell the developer to make changes or provide suggestions if requested.

## Review before pushing

Before pushing code, run the [dd-apm-sdk-review](./.agents/skills/dd-apm-sdk-review/) skill on your unpushed changes. Repo-specific rules live in [`.agents/dd-apm-sdk-review-overrides/`](./.agents/dd-apm-sdk-review-overrides/) — add yours there and cover them with a case in [`.llm-validation/`](./.llm-validation/). See [`.llm-validation/README.md`](./.llm-validation/README.md) for the two-step contribution.

If any `P0` finding is reported, fix it or get explicit authorization and record the unresolved finding verbatim in the PR description. Security findings are never pasted into a PR — route them privately.