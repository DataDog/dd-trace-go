[![Godoc](http://img.shields.io/badge/godoc-reference-blue.svg?style=flat)](https://godoc.org/github.com/DataDog/dd-trace-go/contrib)

The purpose of these packages is to provide tracing on top of commonly used packages from the standard library as well as the 
community in a "plug-and-play" manner. This means that by simply importing the appropriate path, functions are exposed having
 the same signature as the original package. These functions return structures which embed the original return value, allowing 
them to be used as they normally would with tracing activated out of the box.

All of these libraries are supported by our [APM product](https://www.datadoghq.com/apm/).

### Usage

First, find the library which you'd like to integrate with. The naming convention for the integration packages is:

* If the package is from the standard library (eg. `database/sql`), it will be located at the same path.
* If the package is hosted on Github (eg. `github.com/user/repo`), it will be located at the shorthand path `user/repo`.
* If the package is from anywhere else (eg. `google.golang.org/grpc`), it can be found under the full import path.

Each integration comes with thorough documentation and usage examples. A good overview can be seen on our 
[godoc](https://godoc.org/github.com/DataDog/dd-trace-go/contrib) page.
