module github.com/DataDog/dd-trace-go/v2/scripts/configinverter

go 1.23.0

require (
	github.com/DataDog/dd-trace-go/v2 v2.2.0-dev
	github.com/dave/jennifer v1.7.1
)

replace github.com/DataDog/dd-trace-go/v2 => ../..
