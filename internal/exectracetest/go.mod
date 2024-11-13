module gopkg.in/DataDog/dd-trace-go.v1/internal/exectracetest

go 1.22.0

require (
	github.com/google/pprof v0.0.0-20240409012703-83162a5b38cd
	github.com/mattn/go-sqlite3 v1.14.18
	golang.org/x/exp v0.0.0-20240506185415-9bf2ced13842
	gopkg.in/DataDog/dd-trace-go.v1 v1.999.0-beta.10
)

require (
	github.com/DataDog/appsec-internal-go v1.8.0 // indirect
	github.com/DataDog/datadog-agent/pkg/obfuscate v0.57.1 // indirect
	github.com/DataDog/datadog-agent/pkg/remoteconfig/state v0.57.1 // indirect
	github.com/DataDog/datadog-go/v5 v5.5.0 // indirect
	github.com/DataDog/dd-trace-go/contrib/database/sql/v2 v2.0.0-beta.10 // indirect
	github.com/DataDog/dd-trace-go/v2 v2.0.0-beta.10 // indirect
	github.com/DataDog/go-libddwaf/v3 v3.4.0 // indirect
	github.com/DataDog/go-sqllexer v0.0.16 // indirect
	github.com/DataDog/go-tuf v1.1.0-0.5.2 // indirect
	github.com/DataDog/sketches-go v1.4.6 // indirect
	github.com/Microsoft/go-winio v0.6.2 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/eapache/queue/v2 v2.0.0-20230407133247-75960ed334e4 // indirect
	github.com/ebitengine/purego v0.8.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/hashicorp/go-secure-stdlib/parseutil v0.1.8 // indirect
	github.com/hashicorp/go-secure-stdlib/strutil v0.1.2 // indirect
	github.com/hashicorp/go-sockaddr v1.0.7 // indirect
	github.com/mitchellh/mapstructure v1.5.0 // indirect
	github.com/outcaste-io/ristretto v0.2.3 // indirect
	github.com/philhofer/fwd v1.1.3-0.20240916144458-20a13a1f6b7c // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/ryanuber/go-glob v1.0.0 // indirect
	github.com/secure-systems-lab/go-securesystemslib v0.8.0 // indirect
	github.com/tinylib/msgp v1.2.2 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	golang.org/x/mod v0.21.0 // indirect
	golang.org/x/sys v0.26.0 // indirect
	golang.org/x/time v0.7.0 // indirect
	golang.org/x/xerrors v0.0.0-20240903120638-7835f813f4da // indirect
	google.golang.org/protobuf v1.35.1 // indirect
)

// use local version of dd-trace-go
replace gopkg.in/DataDog/dd-trace-go.v1 => ../..
