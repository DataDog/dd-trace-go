package telemetry

// Request is the common high-level structure encapsulating a telemetry request
type Request struct {
	APIVersion  string      `json:"api_version"`
	RequestType RequestType `json:"request_type"`
	TracerTime  int64       `json:"trace_time"`
	RuntimeID   string      `json:"runtime_id"`
	SeqID       int64       `json:"seq_id"`
	Application Application `json:"application"`
	Host        Host        `json:"host"`
	Payload     interface{} `json:"payload"`
}

// RequestType determines how the Payload of a request should be handled
type RequestType string

const (
	// RequestTypeAppStarted is the first message sent by the telemetry
	// client, containing the configuration, and integrations and
	// dependencies loaded at startup
	RequestTypeAppStarted RequestType = "app-started"
	// RequestTypeAppHeartbeat is sent periodically by the client to indicate
	// that the app is still running
	RequestTypeAppHeartbeat RequestType = "app-heartbeat"
	// RequestTypeGenerateMetrics contains all metrics accumulated by the
	// client, and is sent periodically along with the heartbeat
	RequestTypeGenerateMetrics RequestType = "generate-metrics"
	// RequestTypeAppClosing is sent when the telemetry client is stopped
	RequestTypeAppClosing RequestType = "app-closing"
)

// Application is identifying information about the app itself
type Application struct {
	ServiceName     string `json:"service_name"`
	Env             string `json:"env"`
	ServiceVersion  string `json:"service_version,omitempty"`
	TracerVersion   string `json:"tracer_version"`
	LanguageName    string `json:"language_name"`
	LanguageVersion string `json:"language_version"`
}

// Host is identifying information about the host on which the app
// is running
type Host struct {
	ContainerID string `json:"container_id,omitempty"`
	Hostname    string `json:"hostname,omitempty"`
	OS          string `json:"os,omitempty"`
	OSVersion   string `json:"os_version,omitempty"`
	// TODO: Do we care about the kernel stuff? internal/osinfo gets most of
	// this information in OSName/OSVersion
	KernelName    string `json:"kernel_name,omitempty"`
	KernelRelease string `json:"kernel_release,omitempty"`
	KernelVersion string `json:"kernel_version,omitempty"`
}

// AppStarted corresponds to the "app-started" request type
type AppStarted struct {
	Integrations  []Integration   `json:"integrations"`
	Dependencies  []Dependency    `json:"dependencies"`
	Configuration []Configuration `json:"configuration"`
}

// Integration is an integration that is available within the app and applicable
// to be traced
type Integration struct {
	Name        string `json:"name"`
	Enabled     bool   `json:"enabled"`
	Version     string `json:"version,omitempty"`
	AutoEnabled bool   `json:"auto_enabled,omitempty"`
	Compatible  bool   `json:"compatible,omitempty"`
	Error       string `json:"error,omitempty"`
}

// Dependency is a Go module on which the applciation depends. This information
// can be accesed at run-time through the runtime/debug.ReadBuildInfo API.
type Dependency struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Configuration is a library-specific configuration value
type Configuration struct {
	Name string `json:"name"`
	// Value should have a type that can be marshaled to JSON
	Value interface{} `json:"value"`
}

// Metrics corresponds to the "generate-metrics" request type
type Metrics struct {
	Namespace   string   `json:"namespace"`
	LibLanguage string   `json:"lib_language"`
	LibVersion  string   `json:"lib_version"`
	Series      []Series `json:"series"`
}

// Series is a sequence of observations for a single named metric
type Series struct {
	Metric string       `json:"metric"`
	Points [][2]float64 `json:"points"`
	Type   string       `json:"type"`
	Tags   []string     `json:"tags,omitempty"`
	// Common distinguishes metrics which are cross-language vs.
	// language-specific.
	//
	// NOTE: If this field isn't present in the request, the API assumes
	// assumed the metric is common. So we can't "omitempty" even though the
	// field is technically optional.
	Common bool `json:"common"`
}

// TODO: app-dependencies-loaded and app-integrations-change? Does this really
// apply to Go?
