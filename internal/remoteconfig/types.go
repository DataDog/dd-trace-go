// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package remoteconfig

import rc "github.com/DataDog/datadog-agent/pkg/remoteconfig/state"

type clientData struct {
	State        *clientState  `json:"state,omitempty"`
	ClientTracer *clientTracer `json:"client_tracer,omitempty"`
	ID           string        `json:"id,omitempty"`
	Products     []string      `json:"products,omitempty"`
	Capabilities []byte        `json:"capabilities,omitempty"`
	LastSeen     uint64        `json:"last_seen,omitempty"`
	IsTracer     bool          `json:"is_tracer,omitempty"`
}

type clientTracer struct {
	RuntimeID     string   `json:"runtime_id,omitempty"`
	Language      string   `json:"language,omitempty"`
	TracerVersion string   `json:"tracer_version,omitempty"`
	Service       string   `json:"service,omitempty"`
	Env           string   `json:"env,omitempty"`
	AppVersion    string   `json:"app_version,omitempty"`
	Tags          []string `json:"tags,omitempty"`
}

type clientAgent struct {
	Name    string `json:"name,omitempty"`
	Version string `json:"version,omitempty"`
}

type configState struct {
	ID         string        `json:"id,omitempty"`
	Product    string        `json:"product,omitempty"`
	ApplyError string        `json:"apply_error,omitempty"`
	Version    uint64        `json:"version,omitempty"`
	ApplyState rc.ApplyState `json:"apply_state,omitempty"`
}

type clientState struct {
	Error              string         `json:"error,omitempty"`
	ConfigStates       []*configState `json:"config_states,omitempty"`
	BackendClientState []byte         `json:"backend_client_state,omitempty"`
	RootVersion        uint64         `json:"root_version"`
	TargetsVersion     uint64         `json:"targets_version"`
	HasError           bool           `json:"has_error,omitempty"`
}

type targetFileHash struct {
	Algorithm string `json:"algorithm,omitempty"`
	Hash      string `json:"hash,omitempty"`
}

type targetFileMeta struct {
	Path   string            `json:"path,omitempty"`
	Hashes []*targetFileHash `json:"hashes,omitempty"`
	Length int64             `json:"length,omitempty"`
}

type clientGetConfigsRequest struct {
	Client            *clientData       `json:"client,omitempty"`
	CachedTargetFiles []*targetFileMeta `json:"cached_target_files,omitempty"`
}

type clientGetConfigsResponse struct {
	Roots         [][]byte `json:"roots,omitempty"`
	Targets       []byte   `json:"targets,omitempty"`
	TargetFiles   []*file  `json:"target_files,omitempty"`
	ClientConfigs []string `json:"client_configs,omitempty"`
}

type file struct {
	Path string `json:"path,omitempty"`
	Raw  []byte `json:"raw,omitempty"`
}

type fileMetaState struct {
	Hash    string `json:"hash,omitempty"`
	Version uint64 `json:"version,omitempty"`
}
