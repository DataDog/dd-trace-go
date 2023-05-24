package agent

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/version"
)

const (
	defaultHostname = "localhost"
	defaultPort     = "8126"
)

var (
	// defaultSocketAPM specifies the socket path to use for connecting to the trace-agent.
	// Replaced in tests
	defaultSocketAPM = "/var/run/datadog/apm.socket"

	// defaultSocketDSD specifies the socket path to use for connecting to the statsd server.
	// Replaced in tests
	defaultSocketDSD = "/var/run/datadog/dsd.socket"

	defaultHTTPTimeout = 2 * time.Second         // defines the current timeout before giving up with the send process
	traceCountHeader   = "X-Datadog-Trace-Count" // header containing the number of traces in the payload
)

type cfg struct {
	addr   *url.URL
	client *http.Client

	traceHeaders map[string]string
	traceURL     string
	statsURL     string
}

type Option func(c *cfg) error

func newConfig(opts ...Option) cfg {
	c := cfg{}
	c.addr = resolveAgentAddr()

	c.traceHeaders = map[string]string{
		"Datadog-Meta-Lang":             "go",
		"Datadog-Meta-Lang-Version":     strings.TrimPrefix(runtime.Version(), "go"),
		"Datadog-Meta-Lang-Interpreter": runtime.Compiler + "-" + runtime.GOARCH + "-" + runtime.GOOS,
		"Datadog-Meta-Tracer-Version":   version.Tag,
		"Content-Type":                  "application/msgpack",
	}

	for _, o := range opts {
		o(&c)
	}

	if c.addr.Scheme == "unix" {
		c.client = udsClient(c.addr.Path)
		c.addr = &url.URL{
			Scheme: "http",
			Host:   fmt.Sprintf("UDS_%s", strings.NewReplacer(":", "_", "/", "_", `\`, "_").Replace(c.addr.Path)),
		}
	}

	if c.client == nil {
		c.client = defaultClient
	}

	c.traceURL = fmt.Sprintf("%s/v0.4/traces", c.addr.String())
	c.statsURL = fmt.Sprintf("%s/v0.6/stats", c.addr.String())

	return c
}

// WithAddr returns an Option specifying an http or UDS address for
// the agent.
func WithAddr(address string) Option {
	return func(c *cfg) error {
		a, err := url.Parse(address)
		if err != nil {
			return err
		}
		c.addr = a
		return nil
	}
}

// resolveAgentAddr resolves the given agent address and fills in any missing host
// and port using the defaults. Some environment variable settings will
// take precedence over configuration.
func resolveAgentAddr() *url.URL {
	var host, port string
	if v := os.Getenv("DD_AGENT_HOST"); v != "" {
		host = v
	}
	if v := os.Getenv("DD_TRACE_AGENT_PORT"); v != "" {
		port = v
	}
	if agentURL := os.Getenv("DD_TRACE_AGENT_URL"); agentURL != "" {
		u, err := url.Parse(agentURL)
		if err != nil {
			log.Warn("Failed to parse DD_TRACE_AGENT_URL: %v", err)
		} else {
			return u
		}
	}

	if _, err := os.Stat(defaultSocketAPM); host == "" && port == "" && err == nil {
		return &url.URL{
			Scheme: "unix",
			Path:   defaultSocketAPM,
		}
	}
	if host == "" {
		host = defaultHostname
	}
	if port == "" {
		port = defaultPort
	}
	return &url.URL{
		Scheme: "http",
		Host:   fmt.Sprintf("%s:%s", host, port),
	}
}

var defaultDialer = &net.Dialer{
	Timeout:   30 * time.Second,
	KeepAlive: 30 * time.Second,
	DualStack: true,
}

var defaultClient = &http.Client{
	// We copy the transport to avoid using the default one, as it might be
	// augmented with tracing and we don't want these calls to be recorded.
	// See https://golang.org/pkg/net/http/#DefaultTransport .
	Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           defaultDialer.DialContext,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	},
	Timeout: defaultHTTPTimeout,
}

// udsClient returns a new http.Client which connects using the given UDS socket path.
func udsClient(socketPath string) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
				return defaultDialer.DialContext(ctx, "unix", (&net.UnixAddr{
					Name: socketPath,
					Net:  "unix",
				}).String())
			},
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		},
		Timeout: defaultHTTPTimeout,
	}
}
