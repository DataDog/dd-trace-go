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
Please apply the same logic for Pull Requests and Issues: start with the package name, followed by a colon and a description of the change, just like
the official [Go language](https://github.com/golang/go/pulls).

### Style guidelines

A set of [Style guidelines](https://github.com/DataDog/dd-trace-go/wiki/Style-guidelines) was added to our Wiki. Please spend some time browsing it.
It will help tremendously in avoiding comments and speeding up the PR process.


### Integrations

Please view our contrib [README.md](contrib/README.md) for information on new integrations.

### Go Modules

This repository currently takes an [idiosyncratic approach](https://github.com/DataDog/dd-trace-go/issues/810) to using Go modules which means that you should not commit modified versions of the `go.mod` or `go.sum` files.

The following git command can be used to permanently ignore modifications to these files:

```
git update-index --assume-unchanged go.*
```

If you need to undo this for any reason, you can run:

```
git update-index --no-assume-unchanged go.*
```

Additionally there are some [known issues](https://github.com/DataDog/dd-trace-go/issues/911) caused by upstream modules not following semantic versioning. This means you're prone to see errors like this during local development:

```
$ go get -v ./...
# gopkg.in/DataDog/dd-trace-go.v1/contrib/google.golang.org/grpc.v12
contrib/google.golang.org/grpc.v12/grpc.go:61:11: undefined: metadata.FromContext
contrib/google.golang.org/grpc.v12/grpc.go:92:13: undefined: metadata.FromContext
contrib/google.golang.org/grpc.v12/grpc.go:97:9: undefined: metadata.NewContext
# gopkg.in/DataDog/dd-trace-go.v1/contrib/cloud.google.com/go/pubsub.v1
contrib/cloud.google.com/go/pubsub.v1/pubsub.go:34:33: msg.OrderingKey undefined (type *"cloud.google.com/go/pubsub".Message has no field or method OrderingKey)
contrib/cloud.google.com/go/pubsub.v1/pubsub.go:97:34: msg.OrderingKey undefined (type *"cloud.google.com/go/pubsub".Message has no field or method OrderingKey)
```

We're working on a better solution to this problem, but in the meantime you can mitigate the problem by setting this in your local environment, perhaps using [direnv](https://direnv.net/):

```
export GOFLAGS="-tags=localdev $GOFLAGS"
```

### Milestones

The maintainers of this repository assign milestones to pull requests to classify them. `Triage` indicates that it is yet to be decided which version the change will go into. Pull requests that are ready get the upcoming release version assigned.
