[![CircleCI](https://circleci.com/gh/DataDog/dd-trace-go/tree/master.svg?style=svg)](https://circleci.com/gh/DataDog/dd-trace-go/tree/master)
[![Godoc](http://img.shields.io/badge/godoc-reference-blue.svg?style=flat)](https://godoc.org/github.com/DataDog/dd-trace-go/tracer)

A Go tracing package for Datadog APM.

## Contributing Quick Start

Requirements:

* Go 1.7 or later
* Docker
* Rake
* gometalinter – `go get -u github.com/alecthomas/gometalinter`
* golint – `go get -u github.com/golang/lint/golint`
* goconst – `go get -u github.com/jgautheron/goconst/cmd/goconst`
* gas – `go get -u github.com/GoASTScanner/gas`
* ineffassign – `go get -u github.com/gordonklaus/ineffassign`
* gocyclo – `go get -u github.com/fzipp/gocyclo`

### Run the tests

Start the containers defined in `docker-compose.yml`:

```
$ docker-compose up -d
```

Fetch this package's dependencies:

```
$ rake get
```

This will only work if your working directory is in $GOPATH/src.

Finally, run the tests:

```
$ rake ci
```

### Create a Branch

???

## Further Reading

Automatically traced libraries and frameworks: https://godoc.org/github.com/DataDog/dd-trace-go/tracer#pkg-subdirectories
Sample code: https://godoc.org/github.com/DataDog/dd-trace-go/tracer#pkg-examples

