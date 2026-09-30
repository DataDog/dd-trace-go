# Environment Variables

Full reference for adding, validating, and troubleshooting `DD_*` environment
variables in dd-trace-go. For the core rule (never call `os.Getenv`/`os.LookupEnv`
directly), see [CONTRIBUTING.md](./CONTRIBUTING.md#working-with-environment-variables).

Once a new environment variable is added to the codebase, Datadog maintainers will also add it to Datadog's internal configuration registry for tracking and documentation purposes.

Upon each tracer release, new configuration keys are automatically tagged by our [CI pipeline](./.gitlab/config-validation.yml) to track when they were introduced.

## Overriding automatic test retries

`DD_CIVISIBILITY_FLAKY_RETRY_ENABLED` explicitly overrides the automatic test retries setting returned by the CI Visibility backend. When the variable is unset or has an invalid boolean value, the tracer preserves the backend setting. Set it to `true` to enable automatic test retries or `false` to disable them regardless of the backend setting. When the override enables retries that the backend disabled, the backend response provides no retry counts, so the budget comes from `DD_CIVISIBILITY_FLAKY_RETRY_COUNT` (default 5) and `DD_CIVISIBILITY_TOTAL_FLAKY_RETRY_COUNT` (default 1000).

## Code coverage report flags

`DD_CODE_COVERAGE_FLAGS` attaches a comma-separated list of flags to uploaded code coverage reports. Whitespace around each flag is trimmed, empty entries are discarded, and order and duplicate flags are preserved. A maximum of 32 normalized flags is accepted; if the value contains more, the report is uploaded without flags and a warning is logged.

## Adding new environment variables using configinverter

The `configinverter` tool provides a command to add new environment variable keys to the `supported_configurations.json` file and regenerate the corresponding Go code.

```sh
go run ./scripts/configinverter/main.go add DD_MY_NEW_KEY
```

After adding it to the codebase the key also needs to be added to the [registry](https://feature-parity.us1.prod.dog/#/configurations?viewType=configurations) by an **internal contributor**.
If the key already exists in the registry because another language already registred it this step can be skipped.
Not adding the key to the registry will fail the CI step in charge of checking the local file against the registry.

## Auto-detection via tests

All environment variables should be read at least once by a test. When this happens, a helper automatically detects the usage and adds the variable to the [JSON configuration file](./internal/env/supported_configurations.json). Since the variable isn't yet present in the generated code, it won't read any actual environment values initially, but it will be recorded for code generation.

Note that CI jobs will fail if new keys are detected but not properly generated into the code.

You can check for keys that have been added to the JSON file but not yet generated into code:

```sh
go run ./scripts/configinverter/main.go check
```

After the first test run that detects your new environment variable, regenerate the code:

```sh
go run ./scripts/configinverter/main.go generate
```

After adding it to the codebase the key also needs to be added to the [registry](https://feature-parity.us1.prod.dog/#/configurations?viewType=configurations) by an **internal contributor**.
If the key already exists in the registry because another language already registred it this step can be skipped.
Not adding the key to the registry will fail the CI step in charge of checking the local file against the registry.

## Handling related CI failures

The GitLab `validate_supported_configurations_local_file` job validates the JSON file content against Datadog's [configuration registry](https://feature-parity.us1.prod.dog/#/configurations?viewType=configurations) to ensure every configuration key is properly registered and documented. If keys are missing from the registry, the job will fail and display the list of missing keys in the output. These keys must be added to the internal registry by Datadog maintainers for the check to pass, the key will need to be documented before merging the PR onto main.

Additionally, multiple CI jobs include a [step](./.github/actions/supported_configurations_validation/action.yml) that checks for newly discovered environment variables during test execution and will fail if keys are missing from the generated list. To resolve this failure, use one of the two methods described above to add the key to the generated list.
