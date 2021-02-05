package main

import (
	"flag"
	"fmt"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

var ldflags, stack, panicf bool

func main() {
	flag.BoolVar(&stack, "stack", false, "")
	flag.BoolVar(&panicf, "panicf", false, "")
	flag.Parse()
	run()
}

func run() {
	if stack {
		fmt.Printf("debug.Stack:\n%s\n", tracer.Stack())
	}

	// No stack trace attached
	if panicf {
		fmt.Printf("panic:\n%+v\n", recoverPanic(func() { panic("panic") }))
	}
}

func recoverPanic(f func()) (r interface{}) {
	defer func() { r = recover() }()
	f()
	return
}
