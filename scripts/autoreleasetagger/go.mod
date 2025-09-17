module github.com/DataDog/dd-trace-go/v2/scripts/autoreleasetagger

go 1.24.0

require github.com/DataDog/dd-trace-go/v2 v2.4.0-dev

require github.com/Masterminds/semver/v3 v3.4.0 // indirect

replace github.com/DataDog/dd-trace-go/v2 => ../..
