module gopkg.in/DataDog/dd-trace-go.v1/internal/exectracetest

go 1.21

toolchain go1.21.0

require (
	github.com/google/pprof v0.0.0-20230817174616-7a8ec2ada47b
	github.com/mattn/go-sqlite3 v1.14.18
	golang.org/x/exp v0.0.0-20240506185415-9bf2ced13842
	gopkg.in/DataDog/dd-trace-go.v1 v1.64.0
)

require (
	github.com/DataDog/appsec-internal-go v1.7.0 // indirect
	github.com/DataDog/datadog-agent/pkg/obfuscate v0.48.0 // indirect
	github.com/DataDog/datadog-agent/pkg/remoteconfig/state v0.48.1 // indirect
	github.com/DataDog/datadog-go/v5 v5.3.0 // indirect
	github.com/DataDog/go-libddwaf/v3 v3.3.0 // indirect
	github.com/DataDog/go-tuf v1.0.2-0.5.2 // indirect
	github.com/DataDog/sketches-go v1.4.5 // indirect
	github.com/Microsoft/go-winio v0.6.1 // indirect
	github.com/cespare/xxhash/v2 v2.2.0 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/ebitengine/purego v0.6.0-alpha.5 // indirect
	github.com/google/uuid v1.5.0 // indirect
	github.com/outcaste-io/ristretto v0.2.3 // indirect
	github.com/philhofer/fwd v1.1.2 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/secure-systems-lab/go-securesystemslib v0.7.0 // indirect
	github.com/tinylib/msgp v1.1.8 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	golang.org/x/mod v0.17.0 // indirect
	golang.org/x/sync v0.7.0 // indirect
	golang.org/x/sys v0.20.0 // indirect
	golang.org/x/time v0.3.0 // indirect
	golang.org/x/tools v0.21.0 // indirect
	golang.org/x/xerrors v0.0.0-20231012003039-104605ab7028 // indirect
	google.golang.org/protobuf v1.33.0 // indirect
)

// use local version of dd-trace-go
replace gopkg.in/DataDog/dd-trace-go.v1 => ../..
