package declarativeconfig

import (
	"strconv"
	"strings"
)

type declarativeConfig struct {
	fileFormat string `yaml:"file_format" json:"file_format"`
	disabled   bool   `yaml:"disabled" json: "disabled"`
	logLevel   string `yaml:"log_level" json:"log_level"`
	// resourceAttributes map[string]any `yaml:`
}

// TODO: Use otelDDConfigs?
func (c *declarativeConfig) get(key string) string {
	switch key {
	case "file_format":
		return c.fileFormat
	case "disabled":
		return strconv.FormatBool(c.disabled)
	case "DD_TRACE_ENABLED":
		return strconv.FormatBool(!c.disabled)
	case "log_level":
		return c.logLevel
	case "DD_TRACE_DEBUG":
		return strconv.FormatBool(strings.EqualFold("debug", c.logLevel))
	}
	// log about it
	return ""
}
