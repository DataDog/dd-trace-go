# Project overview

## Read CONTRIBUTING.md First

**BEFORE reviewing, writing, or editing ANY code**, you MUST read [CONTRIBUTING.md](./CONTRIBUTING.md) and [README.md](./README.md).

Furthermore, be sure to follow [Effective Go guidelines](https://go.dev/doc/effective_go) when writing Go code.

Run required verification commands directly. If you pipe their output, enable `pipefail` so an earlier failure cannot be masked.

Before reviewing or changing files in these areas, read the applicable scoped instructions:

* [.github/AGENTS.md](./.github/AGENTS.md) -- for updating CI workflows, actions and scripts, in `.github/` and `.gitlab/`
* [contrib/AGENTS.md](./contrib/AGENTS.md) -- for updating contribs/integrations
* [ddtrace/tracer/AGENTS.md](./ddtrace/tracer/AGENTS.md) -- for updating core Datadog tracer implementations and features
* [internal/AGENTS.md](./internal/AGENTS.md) -- for updating features and implementations that are not customer facing
* [profiler/AGENTS.md](./profiler/AGENTS.md) -- for profiling updates

## Adding or changing configuration

Adding, renaming, or changing how an environment variable or configuration key is read MUST follow [Working with environment variables](./CONTRIBUTING.md#working-with-environment-variables). The non-negotiables:

* Tracer configuration belongs in `internal/config`, its single source of truth: follow [internal/config/README.md](./internal/config/README.md) rather than adding a new env read in `ddtrace/tracer`.
* Elsewhere, read keys with `env.Get`/`env.Lookup` from `internal/env` (or `instrumentation/env` in contrib), never `os.Getenv`/`os.LookupEnv`. `make lint` rejects the latter.
* Register each new `DD_*` or `OTEL_*` key in `internal/env/supported_configurations.json` with `go run ./scripts/configinverter/main.go add <KEY>` (or let a test detect it, then run `generate`). Replace the `FIX_ME` type and default placeholders, set `"sensitive": true` for secrets so telemetry omits their values, then rerun `generate` and commit the regenerated `supported_configurations.gen.go`. Unregistered keys read as unset.
* Every key must exist in Datadog's central configuration registry, or the GitLab registry validation job fails. Only an internal contributor can add it there.

## Where documentation goes

Put docs and comments about one product or feature in the most specific place that fits:

1. The godoc of the option, config, or function that enables it.
2. The doc comment or `README.md` of the feature's own package.
3. The `AGENTS.md` of that package, for rules that only apply there.

Don't add product- or feature-specific content to shared files such as the root `README.md`, `CONTRIBUTING.md`, this file, or `ddtrace/tracer/doc.go`. These files grow with every feature, and they must stay short.
