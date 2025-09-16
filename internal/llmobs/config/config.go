package config

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/url"
	"time"
)

type TracerConfig struct {
	DDTags        map[string]any
	Env           string
	Service       string
	Version       string
	AgentURL      *url.URL
	APIKey        string
	APPKey        string
	HTTPClient    *http.Client
	Site          string
	SkipSSLVerify bool
}

type AgentFeatures struct {
	EVPProxyV2 bool
}

type Config struct {
	Enabled               bool
	SampleRate            float64
	MLApp                 string
	AgentlessEnabled      *bool
	InstrumentedProxyURLs []string
	ProjectName           string
	TracerConfig          TracerConfig
	AgentFeatures         AgentFeatures
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

func (c *Config) DefaultHTTPClient(agentless bool) *http.Client {
	var cl *http.Client
	if agentless || c.TracerConfig.AgentURL.Scheme != "unix" {
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
						Name: c.TracerConfig.AgentURL.Path,
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
	if c.TracerConfig.SkipSSLVerify {
		cl.Transport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	return cl
}
