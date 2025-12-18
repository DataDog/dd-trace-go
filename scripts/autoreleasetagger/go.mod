module github.com/DataDog/dd-trace-go/v2/scripts/autoreleasetagger

go 1.24.0

require github.com/DataDog/dd-trace-go/v2 v2.5.0-rc.5

require golang.org/x/mod v0.29.0 // indirect

replace github.com/DataDog/dd-trace-go/v2 => ../..
