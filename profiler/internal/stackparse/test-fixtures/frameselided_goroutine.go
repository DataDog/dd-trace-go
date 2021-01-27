//+build ignore

// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package main

import (
	"os"
	"runtime/pprof"
)

func main() {
	wait := make(chan struct{})
	go stackDump(100, wait)
	<-wait
}

func stackDump(remaining int, done chan struct{}) {
	if remaining > 0 {
		stackDump(remaining-1, done)
	} else {
		pprof.Lookup("goroutine").WriteTo(os.Stdout, 2)
		done <- struct{}{}
	}
}
