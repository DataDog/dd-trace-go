# dd-trace-go

[![CircleCI](https://circleci.com/gh/DataDog/dd-trace-go.svg?style=svg&circle-token=dafe5c53a48e2719deeaf28b61f8d46e740c9c25)](https://circleci.com/gh/DataDog/dd-trace-go)

A Go tracing package. Currently requires at least Go 1.7.

## Docs

While we're in private beta, you can run your own godoc server:

```bash
# Run the docs.
git clone git@github.com:DataDog/dd-trace-go.git
cd dd-trace-go/tracer
go install ./
godoc -http=:6060 -index=true

# Look at the docs.
curl http://localhost:6060/pkg/github.com/DataDog/dd-trace-go/tracer/
```

## Install

While we're in private beta, please check this repository
into your `vendor` folder. If this doesn't work for you, let us know.
