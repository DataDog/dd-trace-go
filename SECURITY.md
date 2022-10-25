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