# Test Apps

Most of dd-trace-go is tested using unit tests or integration tests.

However, some situations require end to end testing. For example:

* Changes involving the agent, backend and UI.
* Producing screen shots for documentation and marketing purposes.
* Validating that existing features continue to work end-to-end.
* etc.

This directory contains a collection of apps that can be used for such purposes.
It also contains the supporting code and documentation that makes it easy to add
more apps and run them locally or in CI.

## Adding a new app

* Add to apps folder
* Register app and variations in apps_test.go

## Running apps in CI

### Manually

### Nightly

TODO: Write ...

## Running apps locally

```
export DD_API_KEY=<API KEY>
export DD_SITE=datad0g.com

docker compose run --build apps
```