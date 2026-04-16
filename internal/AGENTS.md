# Internal dd-trace-go Implementations

Contains private methods and functionality used by the library itself. Code that is not part of the public API and not intended for direct use by customers.

## Important Modules

### Appsec

Handles application security code. This is commonly refer to as "ASM", "Appsec", or "k9 Security". For more information of implementation details, refer to the [README](./appsec/README.md).

### Config

Handles and controls global configuration values. Enables Getting and Setting values in the config. It also defines potential sources for config changes. For relevant telemetry code, refer to [Telemetry](#telemetry).

### Env

Contains important functions for getting and looking up environment variables. Whenever needed, use `env.Lookup` or `env.Get` instead of built in `os.Getenv` functions. This is also available at [instrumentation/env](../instrumentation/env/) for those packages that cannot import internal modules.

### Locking

Locking functionality that serves as a replacement for `sync.mutex` and similar locking mechanisms. It enables checking for deadlocks and should be used instead of `sync`. For more information, read the [README](./locking/README.md).

### Orchestrion

Contains internal Orchestrion implementations for all supported contribs in [./orchestrion/_integration](./orchestrion/_integration/). This includes GLS (Global Local Storage), work for generating changes to `go.mod` files, and tests for expected automatic traces. For more information, read the [README.md](../internal/orchestrion/_integration/README.md).

### Telemetry

The API, struct types, and other values necessary for:

* Metrics: Support for [Count], [Rate], [Gauge], [Distribution] metrics.
* Logs: Support Debug, Warn, Error logs with tags and stack traces via the subpackage [log] or the [Log] function.
* Product: Start, Stop and Startup errors reporting to the backend
* App Config: Register and change the configuration of the application and declare its origin
* Integration: Loading and errors
* Dependencies: Sending all the dependencies of the application to the backend (for SCA purposes for example)

For more information, read the [README](./telemetry/README.md).
