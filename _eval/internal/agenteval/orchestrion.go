// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package agenteval

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/xeipuuv/gojsonschema"
	"gopkg.in/yaml.v3"
)

// orchestrionSchemaModule is where Orchestrion keeps the JSON schema its own
// loader validates against.
const (
	orchestrionModule     = "github.com/DataDog/orchestrion"
	orchestrionSchemaPath = "internal/injector/config/schema.json"
	// orchestrionSchemaAnchor is a module in the tree that requires Orchestrion,
	// used to resolve which version this checkout pins.
	orchestrionSchemaAnchor = "internal/orchestrion/_integration"
)

// ValidateOrchestrionYAML checks an aspect file against Orchestrion's own JSON
// schema.
//
// Parsing as YAML is not enough. A file can be valid YAML, sit in the right
// place, and still be ignored at build time because it is missing meta.name or
// has a malformed join point. That failure is silent: the build succeeds and
// auto-instrumentation simply does nothing, which is exactly the failure mode
// worth scoring.
//
// The schema comes from the Orchestrion version the workspace pins rather than
// a copy vendored here, so it cannot drift from what actually enforces it.
func ValidateOrchestrionYAML(ctx context.Context, workspace, rel string) error {
	target, err := safeJoin(workspace, rel)
	if err != nil {
		return err
	}
	body, err := os.ReadFile(target)
	if err != nil {
		return fmt.Errorf("read %s: %w", rel, err)
	}

	// yaml.v3 decodes mappings with string keys, so the result is already
	// JSON-compatible; round-tripping through JSON keeps gojsonschema happy
	// about numeric types.
	var doc any
	if err := yaml.Unmarshal(body, &doc); err != nil {
		return fmt.Errorf("%s is not valid YAML: %w", rel, err)
	}
	encoded, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("%s does not convert to JSON: %w", rel, err)
	}

	schemaPath, err := orchestrionSchemaFile(ctx, workspace)
	if err != nil {
		return err
	}
	schema, err := gojsonschema.NewSchema(gojsonschema.NewReferenceLoader("file://" + schemaPath))
	if err != nil {
		return fmt.Errorf("load orchestrion schema: %w", err)
	}
	res, err := schema.Validate(gojsonschema.NewBytesLoader(encoded))
	if err != nil {
		return fmt.Errorf("validate %s: %w", rel, err)
	}
	if res.Valid() {
		return nil
	}

	msgs := make([]string, 0, len(res.Errors()))
	for _, e := range res.Errors() {
		msgs = append(msgs, e.String())
	}
	return fmt.Errorf("%s does not conform to the orchestrion schema: %s", rel, strings.Join(msgs, "; "))
}

// orchestrionSchemaFile locates schema.json inside the Orchestrion version the
// workspace depends on. Resolution runs from a module that requires it, since
// the repository root does not.
func orchestrionSchemaFile(ctx context.Context, workspace string) (string, error) {
	anchor, err := safeJoin(workspace, orchestrionSchemaAnchor)
	if err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, "go", "mod", "download", "-json", orchestrionModule)
	cmd.Dir = anchor
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("resolve %s from %s: %w", orchestrionModule, orchestrionSchemaAnchor, err)
	}
	var module struct {
		Dir   string
		Error string
	}
	if err := json.Unmarshal(out, &module); err != nil {
		return "", fmt.Errorf("decode %s module metadata: %w", orchestrionModule, err)
	}
	if module.Error != "" {
		return "", fmt.Errorf("resolve %s from %s: %s", orchestrionModule, orchestrionSchemaAnchor, module.Error)
	}
	if module.Dir == "" {
		return "", fmt.Errorf("%s resolved to an empty module dir", orchestrionModule)
	}
	path := filepath.Join(module.Dir, orchestrionSchemaPath)
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("orchestrion schema not found at %s: %w", path, err)
	}
	return path, nil
}
