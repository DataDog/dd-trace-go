# Config Inverter

This tool transforms the content of a `supported_configurations.json` file into Go code.

The `supported_configurations.json` file contains the list of known environment variables used in `dd-trace-go`, alongside their versions, aliases and telemetry keys.
If a variable is not present in the file and the generated code it won't never be read and will always return `""`.

## 🛠️ Usage

Using `internal/env` package is the only way to read env using `env.Lookup` or `env.Get` as `os.Getenv` and `os.LookupEnv` are forbidden via linter rules. This packages checks the existence of the variable read against a generated list in `internal/env/supported_configurations.gen.go`


### Generate `supported_configurations.gen.go`

From the root of `dd-trace-go` run the following command to generate the file:

```sh
go run ./scripts/configinverter/main.go generate
```

### Check for missing generated keys

Keys are automatically added to the JSON file when detected in a `go test` scenario. After running test
you can check for differences between the content of the generated code and the content of the `supported_configurations.json` in order to make sure that no new env var has been detected.

```sh
go run ./scripts/configinverter/main.go check
```

Example outputs:

```log
2025/08/06 15:05:15 INFO read file file=./internal/env/supported_configurations.json
2025/08/06 15:05:15 INFO supported configuration keys in JSON file count=195
2025/08/06 15:05:15 INFO supported configuration keys in generated map count=195
2025/08/06 15:05:15 INFO supported configurations JSON file and generated map are in sync
2025/08/06 15:05:15 INFO success executing command command=check
```

```log
2025/08/06 15:06:18 INFO read file file=./internal/env/supported_configurations.json
2025/08/06 15:06:18 INFO supported configuration keys in JSON file count=197
2025/08/06 15:06:18 INFO supported configuration keys in generated map count=195
2025/08/06 15:06:18 ERROR supported configuration key not found in generated map key=DD_ANOTHER_NEW_ENV_VAR
2025/08/06 15:06:18 ERROR supported configuration key not found in generated map key=DD_NEW_ENV_VAR
2025/08/06 15:06:18 ERROR supported configuration keys missing in generated map count=2 keys="[DD_ANOTHER_NEW_ENV_VAR DD_NEW_ENV_VAR]"
2025/08/06 15:06:18 INFO run `go run ./scripts/configinverter generate` to re-generate the supported configurations map with the missing keys
2025/08/06 15:06:18 ERROR error executing command error="supported configuration keys missing in generated map"
exit status 1
```

> Tests using an unknown env var should not pass as no value is read from the env when the variable is not known.
If your test passes it's probably not testing what it should.


## 📖 Help

[embedmd]:# (tmp/help.txt)
```txt
Usage of ./configinverter:
  -input string
    	Path to the input file (default "./internal/env/supported_configurations.json")
  -output string
    	Path to the output directory (default "./internal/env")
```
