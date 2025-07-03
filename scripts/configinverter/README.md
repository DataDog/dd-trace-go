# Config Inverter

This tool transforms the content of a `supported-configurations.json` file into Go code.

A `supported-configurations.json` file contains the list of known environment variables, their
versions and aliases used in `dd-trace-go`. If a variable is not present in the file
(or the generated code) it won't be usable.


## 🛠️ Usage

Run the following command to generate the :

```sh
go run ./scripts/configinverter generate
```

Check for differences between the actual content of the generated
code and the content of the `supported-configurations.json`

```sh
go run ./scripts/configinverter check
```

## 📖 Help

[embedmd]:# (tmp/help.txt)
```txt
Usage of ./configinverter:
  -input string
    	Path to the input file (default "./internal/env/supported-configurations.json")
  -output string
    	Path to the output directory (default "./internal/env")
```
