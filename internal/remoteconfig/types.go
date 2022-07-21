package remoteconfig

type ClientData struct {
	State        *ClientState  `json:"state,omitempty"`
	Id           string        `json:"id,omitempty"`
	Products     []string      `json:"products,omitempty"`
	IsTracer     bool          `json:"is_tracer,omitempty"`
	ClientTracer *ClientTracer `json:"client_tracer,omitempty"`
	LastSeen     uint64        `json:"last_seen,omitempty"`
}

type ClientTracer struct {
	RuntimeId     string   `json:"runtime_id,omitempty"`
	Language      string   `json:"language,omitempty"`
	TracerVersion string   `json:"tracer_version,omitempty"`
	Service       string   `json:"service,omitempty"`
	Env           string   `json:"env,omitempty"`
	AppVersion    string   `json:"app_version,omitempty"`
	Tags          []string `json:"tags,omitempty"`
}

type ClientAgent struct {
	Name    string `json:"name,omitempty"`
	Version string `json:"version,omitempty"`
}

type ConfigState struct {
	Id      string `json:"id,omitempty"`
	Version uint64 `json:"version,omitempty"`
	Product string `json:"product,omitempty"`
}

type ClientState struct {
	RootVersion        uint64         `json:"root_version"`
	TargetsVersion     uint64         `json:"targets_version"`
	ConfigStates       []*ConfigState `json:"config_states,omitempty"`
	HasError           bool           `json:"has_error,omitempty"`
	Error              string         `json:"error,omitempty"`
	BackendClientState []byte         `json:"backend_client_state,omitempty"`
}

type TargetFileHash struct {
	Algorithm string `json:"algorithm,omitempty"`
	Hash      string `json:"hash,omitempty"`
}

type TargetFileMeta struct {
	Path   string            `json:"path,omitempty"`
	Length int64             `json:"length,omitempty"`
	Hashes []*TargetFileHash `json:"hashes,omitempty"`
}

type ClientGetConfigsRequest struct {
	Client            *ClientData       `json:"client,omitempty"`
	CachedTargetFiles []*TargetFileMeta `json:"cached_target_files,omitempty"`
}

type ClientGetConfigsResponse struct {
	Roots         [][]byte `json:"roots,omitempty"`
	Targets       []byte   `json:"targets,omitempty"`
	TargetFiles   []*File  `json:"target_files,omitempty"`
	ClientConfigs []string `json:"client_configs,omitempty"`
}

type File struct {
	Path string `json:"path,omitempty"`
	Raw  []byte `json:"raw,omitempty"`
}

type FileMetaState struct {
	Version uint64 `json:"version,omitempty"`
	Hash    string `json:"hash,omitempty"`
}
