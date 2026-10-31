# Project overview

## Read CONTRIBUTING.md First

**BEFORE reviewing, writing, or editing ANY code**, you MUST read [CONTRIBUTING.md](./CONTRIBUTING.md) and [README.md](./README.md).

Furthermore, be sure to follow [Effective Go guidelines](https://go.dev/doc/effective_go) when writing Go code.

Run required verification commands directly. If you pipe their output, enable `pipefail` so an earlier failure cannot be masked.

Before reviewing or changing files in these areas, read the applicable scoped instructions:

* [.github/AGENTS.md](./.github/AGENTS.md) -- for updating CI workflows, actions and scripts
* [contrib/AGENTS.md](./contrib/AGENTS.md) -- for updating contribs/integrations
* [ddtrace/tracer/AGENTS.md](./ddtrace/tracer/AGENTS.md) -- for updating core Datadog tracer implementations and features
* [internal/AGENTS.md](./internal/AGENTS.md) -- for updating features and implementations that are not customer facing
* [internal/config/AGENTS.md](./internal/config/AGENTS.md) -- for adding, migrating, or reviewing tracer configuration fields, including migrations that touch `ddtrace/tracer` or `globalconfig`
* [profiler/AGENTS.md](./profiler/AGENTS.md) -- for profiling updates

## Where documentation goes

Put docs and comments about one product or feature in the most specific place that fits:

1. The godoc of the option, config, or function that enables it.
2. The doc comment or `README.md` of the feature's own package.
3. The `AGENTS.md` of that package, for rules that only apply there.

Don't add product- or feature-specific content to shared files such as the root `README.md`, `CONTRIBUTING.md`, this file, or `ddtrace/tracer/doc.go`. These files grow with every feature, and they must stay short.
