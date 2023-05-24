package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// AgentFeatures holds information about the trace-agent's capabilities.
// When running WithLambdaMode, a zero-value of this struct will be used
// as features.
type AgentFeatures struct {
	// DropP0s reports whether it's ok for the tracer to not send any
	// P0 traces to the agent.
	DropP0s bool

	// Stats reports whether the agent can receive client-computed stats on
	// the /v0.6/stats endpoint.
	Stats bool

	// StatsdPort specifies the Dogstatsd port as provided by the agent.
	// If it's the default, it will be 0, which means 8125.
	StatsdPort int

	// featureFlags specifies all the feature flags reported by the trace-agent.
	featureFlags map[string]struct{}
}

// HasFlag reports whether the agent has set the feat feature flag.
func (a *AgentFeatures) HasFlag(feat string) bool {
	_, ok := a.featureFlags[feat]
	return ok
}

func (a *AgentFeatures) Flags() map[string]struct{} {
	// TODO(knusbaum): Make a copy?
	return a.featureFlags
}

func (a *Agent) LoadFeatures() error {
	resp, err := a.conf.client.Get(fmt.Sprintf("%s/info", a.conf.addr.String()))
	if err != nil {
		return fmt.Errorf("Failed to load Agent Features: %w", err)
	}
	if resp.StatusCode == http.StatusNotFound {
		// agent is older than 7.28.0, features not discoverable
		return errors.New("Failed to load Agent Features. Agent too old.")
	}
	defer resp.Body.Close()
	type infoResponse struct {
		Endpoints     []string `json:"endpoints"`
		ClientDropP0s bool     `json:"client_drop_p0s"`
		StatsdPort    int      `json:"statsd_port"`
		FeatureFlags  []string `json:"feature_flags"`
	}
	var info infoResponse
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return fmt.Errorf("Failed to load Agent Features: %w", err)
	}
	a.features.DropP0s = info.ClientDropP0s
	a.features.StatsdPort = info.StatsdPort
	for _, endpoint := range info.Endpoints {
		switch endpoint {
		case "/v0.6/stats":
			a.features.Stats = true
		}
	}
	a.features.featureFlags = make(map[string]struct{}, len(info.FeatureFlags))
	for _, flag := range info.FeatureFlags {
		a.features.featureFlags[flag] = struct{}{}
	}
	return nil
}

// Features will return the set of AgentFeatures that the agent returns from its Info endpoint.
// This info must be loaded with LoadFeatures before it returns any actual info from the agent.
func (a *Agent) Features() *AgentFeatures {
	return &a.features
}
