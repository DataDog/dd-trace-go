module github.com/DataDog/dd-trace-go/v2/scripts/configinverter

go 1.25.0

require (
	github.com/DataDog/dd-trace-go/v2 v2.9.0-dev
	github.com/dave/jennifer v1.7.1
)

require golang.org/x/mod v0.33.0 // indirect

replace github.com/DataDog/dd-trace-go/v2 => ../..
