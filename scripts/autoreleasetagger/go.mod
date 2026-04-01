module github.com/DataDog/dd-trace-go/v2/scripts/autoreleasetagger

go 1.25.0

require github.com/DataDog/dd-trace-go/v2 v2.9.0-dev

require golang.org/x/mod v0.33.0 // indirect

replace github.com/DataDog/dd-trace-go/v2 => ../..
