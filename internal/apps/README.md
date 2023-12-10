# Test Apps

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
* Update [/.github/workflows/test-apps.cue](/.github/workflows/test-apps.cue) to register the new scenarios and follow the instructions for regenerating the github actions yaml from it.

## Running apps in CI

### Manually

TODO: Describe

### Nightly

All test scenarios are run nightly in CI.

## Running apps locally

```
export DD_API_KEY=<API KEY>
docker-compose run --build scenario memory-leak/heap
```

Note:
* The default destination site is prod. Set `export DD_SITE=datad0g.com` to send the data to staging.
* You can pass `-e DD_TEST_APPS_TOTAL_DURATION=120s` and similar vars to `docker-compose`, see [scenario_test.go](./scenario_test.go) for the available vars.