// +build ignore

// usage: go run stack2pprof.go < stack.txt > stack.pprof

// TODO(fg) remove this, just useful for local testing

package main

import (
	"os"

	"gopkg.in/DataDog/dd-trace-go.v1/profiler"
)

func main() {
	goroutines, err := profiler.ParseGoroutineDebug2Profile(os.Stdin)
	if err != nil {
		panic(err)
	} else if err := profiler.MarshalGoroutineDebug2Profile(os.Stdout, goroutines); err != nil {
		panic(err)
	}
}
