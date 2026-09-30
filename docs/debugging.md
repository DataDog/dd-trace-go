# Debugging

## Goroutine Leaks

Some core packages are using [uber-go/goleak](https://github.com/uber-go/goleak) to detect goroutine leaks.

To isolate the leak to a single test, you can use the bash script from the goleak README.

If you are experiencing a leak failure in CI that doesn't seem to reproduce locally, try running a local datadog agent. Some test failures only appear when http connections to the agent are created and become idle after the test completes.

Last but not least, you might find a goroutine leak with an unhelpful stack trace:

```
Goroutine 92554 in state IO wait, with internal/poll.runtime_pollWait on top of the stack:
internal/poll.runtime_pollWait(0x7f46dcd5b368, 0x72)
 /opt/hostedtoolcache/go/1.23.11/x64/src/runtime/netpoll.go:351 +0x85
...
net/http.(*persistConn).readLoop(0xc0041c8b40)
 /opt/hostedtoolcache/go/1.23.11/x64/src/net/http/transport.go:2205 +0x354
created by net/http.(*Transport).dialConn in goroutine 92609
 /opt/hostedtoolcache/go/1.23.11/x64/src/net/http/transport.go:1874 +0x29b4
```

In this case, consider editing the stdlib code (e.g. `http/transport.go:1874`) to print a stack trace at the location where the goroutine is being created:

```go
fmt.Printf("Leak start at stack=%s\n", string(debug.Stack()))
```

In practice, leaks often go through `http.(*Client).Do`, so that can be a good place to instrument as well.

Following the advice above, most goroutine leaks should be easy to debug and fix.
