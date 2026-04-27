# Orchestrion Implementations

Orchestrion is a project at [github.com/DataDog/orchestrion](https://github.com/DataDog/orchestrion) that enables auto-instrumentation at compile time for dd-trace-go. The functionality that supports Orchestrion lives in this other project. Within dd-trace-go, Orchestrion exists in several places:

1. `./orchestrion` -- This directory, including a list of all supported integrations and auto generated files
2. [../internal/orchestrion](../internal/orchestrion/) -- Internal functionality for Orchestrion, including testing data for expected traces after instrumentation. For more information, read the [README.md](../internal/orchestrion/_integration/README.md).
3. [../contrib](../contrib/) -- Each integration that supports auto-instrumentation has its own `orchestrion.yml` file that defines where and how traces are created. 

Orchestrion uses Aspect-Oriented Programming (AOP) and the Go `toolexec` command to identify which nodes to update and how to create traces from those nodes. It processes Go source code at compilation time and automatically inserts instrumentation. This instrumentation is driven by the imports present in the orchestrion.tool.go file at the project's root.

For more information on how to use Orchestrion in a user's project, refer to the [user guide](https://datadoghq.dev/orchestrion/docs/getting-started/). 

## Contributing

For references on which aspects and join points are available, code templates, and other contributing guidelines, refer to the [contributor guide](https://datadoghq.dev/orchestrion/contributing/aspects/). 

### Key Takeaways

Orchestrion uses the Go `text/template` module to render and read YAML files.

For different contexts, use:

* `{{ Version }}` to get the current Orchestrion version
* `{{ . }}` to get the value of the current, active AST node matched by the join point
* `{{ .DirectiveArgs }}` to get a key (`{{ .Key }}`)/value (`{{ .Value }}`) list of directives
* `{{ .Function }}` to get information about a function grabbed by a join point
  * `{{ .Function.Name }}: function’s name, or a blank string
  * `{{ .Function.Receiver }}`: name of the receiver value for this function. Returns an error if the surrounding function is not a method.
  * `{{ .Function.Argument n }}`: name of the nth argument (0-based) of the function. Returns an error if the surrounding function does not have enough arguments.
  * `{{ .Function.ArgumentOfType type }}`: name of the first argument that has the specified type; or a blank string if no such argument exists.
  * `{{ .Function.Result n }}`: name of the nth return value (0-based) of the function. Returns an error if the surrounding function does not have enough return values.
  * `{{ .Function.ResultOfType type }}`: name of the first result value that has the specified type; or a blank string if no such result value exists.

When adding new code in an advice, strive to keep it as minimal as possible and only use public, exposed APIs.

### Aspects

`orchestrion.yml` files contain aspects in the following format:

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