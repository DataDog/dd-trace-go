package config

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal"
)

const (
	EnvEnabled               = "DD_LLMOBS_ENABLED"
	EnvSampleRate            = "DD_LLMOBS_SAMPLE_RATE"
	EnvMlApp                 = "DD_LLMOBS_ML_APP"
	EnvAgentlessEnabled      = "DD_LLMOBS_AGENTLESS_ENABLED"
	EnvInstrumentedProxyUrls = "DD_LLMOBS_INSTRUMENTED_PROXY_URLS"
	EnvProjectName           = "DD_LLMOBS_PROJECT_NAME"
)

type Config struct {
	Enabled               bool
	SampleRate            float64
	MLApp                 string
	AgentlessEnabled      bool
	InstrumentedProxyURLs []string
	ProjectName           string

	AgentURL      *url.URL
	APIKey        string
	APPKey        string
	HTTPClient    *http.Client
	Site          string
	SkipSSLVerify bool
}

// We copy the transport to avoid using the default one, as it might be
// augmented with tracing and we don't want these calls to be recorded.
// See https://golang.org/pkg/net/http/#DefaultTransport .
func newHTTPClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   30 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		},
		Timeout: 5 * time.Second,
	}
}

func Default() *Config {
	return &Config{
		Enabled:               internal.BoolEnv(EnvEnabled, false),
		SampleRate:            internal.FloatEnv(EnvSampleRate, 1.0),
		MLApp:                 os.Getenv(EnvMlApp),
		AgentlessEnabled:      internal.BoolEnv(EnvAgentlessEnabled, false),
		InstrumentedProxyURLs: instrumentedProxyURLsFromEnv(),
		ProjectName:           os.Getenv(EnvProjectName),
		AgentURL:              internal.AgentURLFromEnv(),
		APIKey:                os.Getenv("DD_API_KEY"),
		APPKey:                os.Getenv("DD_APP_KEY"),
		HTTPClient:            nil,
		Site:                  os.Getenv("DD_SITE"),
		SkipSSLVerify:         internal.BoolEnv("DD_SKIP_SSL_VALIDATION", false),
	}
}

func (c *Config) DefaultHTTPClient() *http.Client {
	var cl *http.Client
	if c.AgentlessEnabled || c.AgentURL.Scheme != "unix" {
		cl = newHTTPClient()
	} else {
		dialer := &net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}
		cl = &http.Client{
			Transport: &http.Transport{
				Proxy: http.ProxyFromEnvironment,
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return dialer.DialContext(ctx, "unix", (&net.UnixAddr{
						Name: c.AgentURL.Path,
						Net:  "unix",
					}).String())
				},
				MaxIdleConns:          100,
				IdleConnTimeout:       90 * time.Second,
				TLSHandshakeTimeout:   10 * time.Second,
				ExpectContinueTimeout: 1 * time.Second,
			},
			Timeout: 10 * time.Second,
		}
	}
	if c.SkipSSLVerify {
		cl.Transport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	return cl
}

func instrumentedProxyURLsFromEnv() []string {
	v := os.Getenv(EnvInstrumentedProxyUrls)
	if v == "" {
		return nil
	}
	seen := make(map[string]struct{})
	out := make([]string, 0)
	for _, part := range strings.Split(v, ",") {
		s := strings.TrimSpace(part)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
