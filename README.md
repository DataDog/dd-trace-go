# dd-trace-go

[![CircleCI](https://circleci.com/gh/DataDog/dd-trace-go/tree/master.svg?style=svg)](https://circleci.com/gh/DataDog/dd-trace-go/tree/master)
[![Godoc](http://img.shields.io/badge/godoc-reference-blue.svg?style=flat)](https://godoc.org/github.com/DataDog/dd-trace-go/tracer)

The Datadog go tracing package. Currently requires at least Go 1.7.

Sample code: https://godoc.org/github.com/DataDog/dd-trace-go/tracer#pkg-examples  
List of integrations: https://godoc.org/github.com/DataDog/dd-trace-go/tracer#pkg-subdirectories

## Directory structure

- [tracer](https://github.com/DataDog/dd-trace-go/tree/gabin/contributions/tracer): contains the low level API used to trace the different libraries.

- [libs](https://github.com/DataDog/dd-trace-go/tree/gabin/contributions/libs): contains the different libraries supported by our APM solution.
