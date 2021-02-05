package main

import (
	"fmt"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

func main() {
	run()
}

func run() {
	fmt.Printf("debug.Stack:\n%s\n", tracer.Stack())
}
