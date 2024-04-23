module github.com/datadog/dd-trace-go/dyngo

go 1.20

replace gopkg.in/DataDog/dd-trace-go.v1 => ../

require (
	github.com/dave/jennifer v1.7.0
	github.com/stretchr/testify v1.9.0
	go.uber.org/atomic v1.11.0
	golang.org/x/tools v0.20.0
	gopkg.in/DataDog/dd-trace-go.v1 v1.0.0-00010101000000-000000000000
)

require (
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/philhofer/fwd v1.1.2 // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	github.com/tinylib/msgp v1.1.8 // indirect
	golang.org/x/mod v0.17.0 // indirect
	golang.org/x/sync v0.7.0 // indirect
	gopkg.in/check.v1 v1.0.0-20201130134442-10cb98267c6c // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)
