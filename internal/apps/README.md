# Internal Test Apps

Most of dd-trace-go is tested using unit tests or integration tests.

However, some situations require end to end testing. For example:

* Changes involving the agent, backend and UI.
* Producing screen shots for documentation and marketing purposes.
* Validating that existing features continue to work end-to-end.
* etc.

This directory contains a collection of apps that can be used for such purposes. It also contains the supporting code and documentation that makes it easy to add more apps and run them locally or in CI.

## Adding a new app

* Copy an existing app directory and rename it.
* Define your test scenarios [scenario_test.go](./scenario_test.go).
* Update [/.github/workflows/test-apps.cue](/.github/workflows/test-apps.cue) to register the new scenarios and run `make test-apps.yml`.

## Run the apps

### Manually via CI

1. Follow [this link](https://github.com/DataDog/dd-trace-go/actions/workflows/test-apps.yml) to open the test workflow in GH Actions.
2. Click `Run workflow`
3. Select the scenarios you want to run.
4. Configure any other parameters you want to change.
5. Press `Run workflow`

### Scheduled via CI

All test scenarios are run nightly for 10min and weekly for 1h in CI.

## Local development

```
export DD_API_KEY=<API KEY>
docker-compose run --build scenario memory-leak/heap$
```

Note:
* The default destination site is prod. Set `export DD_SITE=datad0g.com` to send the data to staging.
* You can pass `-e DD_TEST_APPS_TOTAL_DURATION=120s` and similar vars to `docker-compose`, see [scenario_test.go](./scenario_test.go) for the available vars.

# Cost

The CI cost of adding a new scenario is calculated as follows:

```
envs * nightly_minutes * cost_per_minute * 30 + envs * weekly_minutes * cost_per_minute * 4
2 * 10 * 0.008 * 30 + 2 * 60 * 0.008 * 4 = $8.64
```

See GH Actions [per-minute-rates][] for more details.

[per-minute-rates]: https://docs.github.com/en/billing/managing-billing-for-github-actions/about-billing-for-github-actions#per-minute-rates