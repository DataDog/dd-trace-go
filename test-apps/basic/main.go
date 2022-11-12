package main

import "gopkg.in/DataDog/dd-trace-go.v1/profiler"

func main() {
    err := profiler.Start()
    if err != nil {
        panic(err)
    }
    defer profiler.Stop()

    for true {
        a()
        b()
    }
}

func a() {
    x := 0
    for i := 0; i < 1000000; i += 1 {
        x += i
    }
}

func b() {
    x := 0
    for i := 0; i < 1000000; i += 1 {
        x += i
    }
}
