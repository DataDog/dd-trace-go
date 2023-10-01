# Security Policy

## Supported Versions

Please see our [Support Policy](README.md#support-policy)

## Reporting a Vulnerability

Please note that we rely on golang.org/x/vuln/vulncheck to indicate whether any of our dependencies have a vulnerability that could impact any of the users of our APIs.
We have chosen this tool since it works differently from other vulnerability scanners.
From the [blog post](https://go.dev/blog/vuln): "Govulncheck analyzes your codebase and only surfaces vulnerabilities that actually affect you, based on which functions in your code are transitively calling vulnerable functions."

It's possible that other vulnerability scanners could report that our codebase has vulnerabilities.
However, these scanners can report false positives if the vulnerabilities affect functions that our code doesn't use.

Given this information, please file an issue if you feel that golang.org/x/vuln/vulncheck has given a false negative and that our code is using a vulnerable function such that users of our code could be affected.

If you have found a security issue in our code directly, please contact the security team at security@datadoghq.com, rather than filing a public issue.

## Vulnerabilities in Contrib Dependencies

If you are using a vulnerability checker other than `golang.org/x/vuln/vulncheck` you may detect vulnerabilities in our contrib dependencies.
In general we like to specify non-vulnerable minimum versions of dependencies when we can do so in a non-breaking way. To avoid breaking users of this library
there may be contrib libraries that are deprecated/vulnerable but still appear in our go.mod file. If you are not using these contrib packages you are not vulnerable (i.e. if they do not appear in your go.sum file).
At the next major version we will drop support for these packages. (e.g. as of dd-trace-go@v1 labstack/echo v3 is considered deprecated and users should migrate to labstack/echo.v4)

Note that since library go.mod files only specify minimum version requirements you are welcome to specify a newer version of any dependencies to satisfy your tooling.
For example, if you would like to require a library like `github.com/labstack/echo/v4` use version v4.10.0 you can do so by running `go get github.com/labstack/echo/v4@v4.10.0`.
Additional documentation and details can be found [here](https://go.dev/ref/mod#go-get).
