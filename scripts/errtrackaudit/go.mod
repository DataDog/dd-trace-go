module github.com/DataDog/dd-trace-go/v2/scripts/errtrackaudit

go 1.26.0

require golang.org/x/tools v0.49.0

require (
	github.com/DataDog/dd-trace-go/v2 v2.12.0-dev.2
	golang.org/x/mod v0.40.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
)

replace github.com/DataDog/dd-trace-go/v2 => ../..
