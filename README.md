[![CircleCI](https://circleci.com/gh/DataDog/dd-trace-go/tree/v1.svg?style=svg)](https://circleci.com/gh/DataDog/dd-trace-go/tree/v1)
[![Godoc](http://img.shields.io/badge/godoc-reference-blue.svg?style=flat)](https://godoc.org/gopkg.in/DataDog/dd-trace-go.v1)

### Installing

```bash
go get gopkg.in/DataDog/dd-trace-go.v1/...
```

Requires:

* Go 1.9
* Datadog's Trace Agent >= 5.21.1


### Testing

The best way to run the tests is using the [CircleCI CLI](https://circleci.com/docs/2.0/local-jobs/). Simply run `circleci build`
in the repository root. Note that you might have to increase the resources dedicated to Docker to around 4GB.

### Contributing

Pull requests for bug fixes are welcome, but before submitting new features or changes to current functionalities [open an issue](https://github.com/DataDog/dd-trace-go/issues/new)
and discuss your ideas or propose the changes you wish to make. After a resolution is reached a PR can be submitted for review.

For commit messages, try to use the same conventions as most Go projects, for example:
```
contrib/database/sql: use method context on QueryContext and ExecContext

QueryContext and ExecContext were using the wrong context to create
spans. Instead of using the method's argument they were using the
Prepare context, which was wrong.

Fixes #113
```
Please apply the same logic for Pull Requests, start with the package name, followed by a colon and a description of the change, just like
the official [Go language](https://github.com/golang/go/pulls).

### Documentation

The API is documented on [godoc](https://godoc.org/gopkg.in/DataDog/dd-trace-go.v1/ddtrace) as well as Datadog's [official documentation](https://docs.datadoghq.com/tracing/setup/go/).
