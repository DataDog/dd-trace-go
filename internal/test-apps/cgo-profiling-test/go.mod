module gopkg.in/DataDog/dd-trace-go.v1/internal/test-apps/cgo-profiling-test

go 1.18

require (
	github.com/mattn/go-sqlite3 v1.14.15
	github.com/nsrip-dd/cgotraceback v0.0.0-20220922153927-5e3bc6c77cff
	gopkg.in/DataDog/dd-trace-go.v1 v1.43.1
)

require (
	github.com/DataDog/datadog-go/v5 v5.0.2 // indirect
	github.com/DataDog/gostackparse v0.5.0 // indirect
	github.com/Microsoft/go-winio v0.5.1 // indirect
	github.com/google/pprof v0.0.0-20210423192551-a2663126120b // indirect
	github.com/google/uuid v1.3.0 // indirect
	github.com/richardartoul/molecule v1.0.1-0.20221107223329-32cfee06a052 // indirect
	github.com/spaolacci/murmur3 v1.1.0 // indirect
	golang.org/x/sys v0.0.0-20220722155257-8c9f86f7a55f // indirect
)

replace gopkg.in/DataDog/dd-trace-go.v1 => ../../../
