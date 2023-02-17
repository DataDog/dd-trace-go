package telemetry

import (
	"net/http"
	"net/url"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

type Option func(*Client)

func WithNamespace(name Namespace) Option {
	return func(client *Client) {
		client.Namespace = name
	}
}
func WithEnv(env string) Option {
	return func(client *Client) {
		client.Env = env
	}
}
func WithService(service string) Option {
	return func(client *Client) {
		client.Service = service
	}
}
func WithVersion(version string) Option {
	return func(client *Client) {
		client.Version = version
	}
}
func WithHTTPClient(httpClient *http.Client) Option {
	return func(client *Client) {
		client.Client = httpClient
	}
}
func WithAPIKey(v string) Option {
	return func(client *Client) {
		client.APIKey = v
	}
}
func WithURL(agentless bool, agentURL string) Option {
	return func(client *Client) {
		if agentless {
			client.URL = "https://instrumentation-telemetry-intake.datadoghq.com/api/v2/apmtelemetry"
		} else {
			// TODO: check agent /info endpoint to see if the agent is
			// sufficiently recent to support this endpoint? overkill?
			u, err := url.Parse(agentURL)
			if err == nil {
				u.Path = "/telemetry/proxy/api/v2/apmtelemetry"
				client.URL = u.String()
			} else {
				log.Warn("Agent URL %s is invalid, not starting telemetry", agentURL)
				client.Disabled = true
			}
		}
	}
}
func WithLogger(logger interface {
	Printf(msg string, args ...interface{})
}) Option {
	return func(client *Client) {
		client.Logger = logger
	}
}
func defaultClient() (client *Client) {
	client = new(Client)
	return client
}
