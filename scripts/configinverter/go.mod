module github.com/DataDog/dd-trace-go/v2/scripts/configinverter

go 1.24.0

require (
	github.com/DataDog/dd-trace-go/v2 v2.5.0-rc.2
	github.com/dave/jennifer v1.7.1
)

require golang.org/x/mod v0.29.0 // indirect

replace github.com/DataDog/dd-trace-go/v2 => ../..
