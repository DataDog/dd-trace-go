module github.com/DataDog/dd-trace-go/v2/scripts/autoreleasetagger

go 1.23.0

require github.com/DataDog/dd-trace-go/v2 v2.2.3-rc.1

require github.com/Masterminds/semver/v3 v3.3.1 // indirect

replace github.com/DataDog/dd-trace-go/v2 => ../..
