module github.com/DataDog/dd-trace-go/v2/scripts/configaudit

go 1.25.0

replace github.com/DataDog/dd-trace-go/v2 => ../..

require golang.org/x/tools v0.45.0

require (
	golang.org/x/mod v0.36.0 // indirect
	golang.org/x/sync v0.20.0 // indirect
)
