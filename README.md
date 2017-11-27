[![CircleCI](https://circleci.com/gh/DataDog/dd-trace-go/tree/master.svg?style=svg)](https://circleci.com/gh/DataDog/dd-trace-go/tree/master)
[![Godoc](http://img.shields.io/badge/godoc-reference-blue.svg?style=flat)](https://godoc.org/github.com/DataDog/dd-trace-go/tracer)

A Go tracing package for Datadog APM.

## Contributing Quick Start

Requirements:

* Go 1.7 or later
* Docker
* Rake
* [gometalinter](https://github.com/alecthomas/gometalinter)

### Run the tests

Start the containers defined in `docker-compose.yml` so that integrations can be tested:

```
$ docker-compose up -d
$ ./wait-for-services.sh  # wait that all services are up and running
```

Fetch package's third-party dependencies (integrations and testing utilities):

```
$ rake init
```

This will only work if your working directory is in $GOPATH/src.

Now, you can run your tests via :

```
$ rake test:lint  # linting via gometalinter
$ rake test:all   # test the tracer and all integrations
$ rake test:race  # use the -race flag
```

## Further Reading

Automatically traced libraries and frameworks: https://godoc.org/github.com/DataDog/dd-trace-go/tracer#pkg-subdirectories
Sample code: https://godoc.org/github.com/DataDog/dd-trace-go/tracer#pkg-examples
