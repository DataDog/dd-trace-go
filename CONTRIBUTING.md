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
