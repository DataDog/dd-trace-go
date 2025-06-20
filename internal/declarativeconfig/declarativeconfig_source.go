package declarativeconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	_ "embed"

	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/xeipuuv/gojsonschema"
	"gopkg.in/yaml.v3"
)

const DD_TRACE_OTEL_CONFIG_FILE_PATH = "DD_TRACE_OTEL_CONFIG_FILE_PATH"

var (
	filePath         = os.Getenv(DD_TRACE_OTEL_CONFIG_FILE_PATH)
	ErrNotConfigured = errors.New(DD_TRACE_OTEL_CONFIG_FILE_PATH + "not configured")
)

var config = newConfig()

//go:embed schema.json
var schemaBytes []byte

func validateConfig(rawYAML []byte) (map[string]interface{}, error) {
	// Step 1: Unmarshal YAML into generic map
	var configData map[string]any
	if err := yaml.Unmarshal(rawYAML, &configData); err != nil {
		return nil, fmt.Errorf("invalid YAML: %v", err)
	}

	// Step 2: Marshal to JSON for validation
	configJSON, err := json.Marshal(configData)
	if err != nil {
		return nil, fmt.Errorf("failed to convert YAML to JSON: %v", err)
	}

	// Step 4: Validate using the schema
	schemaLoader := gojsonschema.NewBytesLoader(schemaBytes)
	configLoader := gojsonschema.NewBytesLoader(configJSON)

	result, err := gojsonschema.Validate(schemaLoader, configLoader)
	if err != nil {
		return nil, fmt.Errorf("validation failed: %v", err)
	}

	if !result.Valid() {
		var errors []string
		for _, desc := range result.Errors() {
			errors = append(errors, desc.String())
		}
		return nil, fmt.Errorf("config is invalid:\n%s", strings.Join(errors, "\n"))
	}

	return configData, nil
}

func readFile(filePath string) ([]byte, error) {
	if filePath == "" {
		return nil, ErrNotConfigured
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("error reading config file: %v", err)
	}
	return data, nil
}

func newConfig() *declarativeConfigMap {
	data, err := readFile(filePath)
	if err != nil {
		// If err is ErrNotConfigured
		if err != ErrNotConfigured {
			log.Debug("Error sourcing declarative configuration: %v", err)
		}
		return nil
	}
	config, err := validateConfig(data)
	if err != nil {
		log.Debug("Error validating declarative configuration schema: %v", err)
		return nil
	}
	configMap := declarativeConfigMap(config)
	return &configMap
}
