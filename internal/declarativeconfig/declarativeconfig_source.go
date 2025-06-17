package declarativeconfig

import (
	"embed"
	"encoding/json"
	"fmt"
	"os"

	"github.com/xeipuuv/gojsonschema"
	"gopkg.in/yaml.v3"
	// "github.com/DataDog/dd-trace-go/v2/internal/log"
)

var filePath = os.Getenv("DD_TRACE_OTEL_CONFIG_FILE_PATH")

var config = parseFile()

//go:embed schema_v040.json
var schemaFS embed.FS

func validateConfig(configJSON []byte) error {
	schemaBytes, err := schemaFS.ReadFile("schema_v040.json")
	if err != nil {
		return fmt.Errorf("failed to read embedded schema: %w", err)
	}

	schemaLoader := gojsonschema.NewBytesLoader(schemaBytes)
	configLoader := gojsonschema.NewBytesLoader(configJSON)

	result, err := gojsonschema.Validate(schemaLoader, configLoader)
	if err != nil {
		return fmt.Errorf("schema validation error: %w", err)
	}

	if !result.Valid() {
		// TODO: Join the errors and return them
		for _, e := range result.Errors() {
			fmt.Printf("Validation error: %s\n", e)
		}
		return fmt.Errorf("configuration is not valid")
	}

	return nil
}

func parseFile() *declarativeConfig {
	if filePath == "" {
		return nil
	}
	rawYaml, err := os.ReadFile(filePath)
	if err != nil {
		// log about it
		return nil
	}
	var dc declarativeConfig
	unmarshal(rawYaml, &dc)
	return &dc
}

func unmarshal(data []byte, dc *declarativeConfig) {
	// Unmarshal YAML into generic map to convert to JSON
	var intermediate map[string]interface{}
	if err := yaml.Unmarshal(data, &intermediate); err != nil {
		// log about it
		return
	}

	jsonData, err := json.Marshal(intermediate)
	if err != nil {
		// log about it
		return
	}

	if err := validateConfig(jsonData); err != nil {
		// log about it
		fmt.Println("Configuration validation failed:", err)
		return
	}

	if err := yaml.Unmarshal(data, dc); err != nil {
		// log about it
		return
	}
}
