# Orchestrion Implementations

Orchestrion is a project at [github.com/DataDog/orchestrion](https://github.com/DataDog/orchestrion) that enables auto-instrumentation at compile time for dd-trace-go. The functionality that supports Orchestrion lives in this other project. Within dd-trace-go, Orchestrion exists in several places:

1. `./orchestrion` -- This directory, including a list of all supported integrations and auto generated files
2. [../internal/orchestrion](../internal/orchestrion/) -- Internal functionality for Orchestrion, including testing data for expected traces after instrumentation. For more information, read the [README.md](../internal/orchestrion/_integration/README.md).
3. [../contrib](../contrib/) -- Each integration that supports auto-instrumentation has its own `orchestrion.yml` file that defines where and how traces are created. 

Orchestrion uses Aspect-Oriented Programming (AOP) and the Go `toolexec` command to identify which nodes to update and how to create traces from those nodes. It processes Go source code at compilation time and automatically inserts instrumentation. This instrumentation is driven by the imports present in the orchestrion.tool.go file at the project's root.

For more information on how to use Orchestrion in a user's project, refer to the [user guide](https://datadoghq.dev/orchestrion/docs/getting-started/). 

## Contributing

For references on which aspects and join points are available, code templates, and other contributing guidelines, refer to the [contributor guide](https://datadoghq.dev/orchestrion/contributing/aspects/). 

Orchestrion uses the Go `text/template` module to render and read YAML files.

### Aspects

`orchestrion.yaml` files contain aspects in the following format:

```yaml
aspects:
  - id: name for the aspect
    join-point:
      <some join point supported by Orchestrion>:
    advice:
      - <some advice supported by Orchestrion>:
          imports:
            <import name>: <import path>
          template: |-
            // Code to inject into the AST Node
```

For available join points, see the [Join Point documentation](https://datadoghq.dev/orchestrion/contributing/aspects/join-points/). For available advices, see the [Advice documentation](https://datadoghq.dev/orchestrion/contributing/aspects/advice/).

### Including Dependency Upgrades

When adding new dependencies or upgrading existing ones, run [fix_modules.sh](../scripts/fix_modules.sh) and [generate.sh](../scripts/generate.sh) to ensure all `go.mod` files and all Orchestrion files are up to date.

### Testing

Testing includes checking that the correct traces appear after instrumentation. See the [internal Orchestrion directory](../internal/orchestrion/_integration/) for testing files and scenarios.