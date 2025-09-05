package internal

import (
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

	AgentURL   *url.URL
	APIKey     string
	APPKey     string
	HTTPClient *http.Client
	Site       string
}

var defaultClient = &http.Client{
	Transport: &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	},
}

func defaultConfig() *Config {
	cfg := &Config{
		Enabled:               internal.BoolEnv(EnvEnabled, false),
		SampleRate:            internal.FloatEnv(EnvSampleRate, 1.0),
		MLApp:                 os.Getenv(EnvMlApp),
		AgentlessEnabled:      internal.BoolEnv(EnvAgentlessEnabled, false),
		InstrumentedProxyURLs: instrumentedProxyURLsFromEnv(),
		ProjectName:           os.Getenv(EnvProjectName),
		AgentURL:              internal.AgentURLFromEnv(),
		APIKey:                os.Getenv("DD_API_KEY"),
		APPKey:                os.Getenv("DD_APP_KEY"),
		HTTPClient:            defaultClient,
		Site:                  os.Getenv("DD_SITE"),
	}
	return cfg
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

type Option func(cfg *Config)

// TODO(rarguelloF): add options
