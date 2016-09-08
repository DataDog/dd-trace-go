# dd-trace-go

A Go tracing package.

## Requirements

* Go version ``X.X``
* [Glide][glide] package manager

[glide]: http://glide.sh/

## Getting started

Checkout the package within your GOPATH, then install ``glide``:

```
$ curl https://glide.sh/get | sh
# or
$ go get github.com/Masterminds/glide
```

Use ``glide`` to fetch project dependencies:

```
$ glide up
```

To run the test suite ``golint`` has to be available on your system, if it is not, just install it with:

```
$ go get -u github.com/golang/lint/golint
```

## Testing

A set of ``rake`` tasks are available to run the test suite and to build the package:

```
$ rake fmt     # formats the source code
$ rake golint  # source code linting
$ rake vet     # static analyzer
$ rake test    # preliminary checks and launch tests!
$ rake build   # build the package
$ rake         # launch all the things! (useful for CI)
```

## Benchmark

Work in progress.
