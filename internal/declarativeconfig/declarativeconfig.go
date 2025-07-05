// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.
package declarativeconfig

type declarativeConfigMap map[string]any

// TODO: Use otelDDConfigs?
func (c *declarativeConfigMap) getString(key string) (string, bool) {
	if c == nil {
		return "", false
	}
	val, ok := (*c)[key]
	if !ok {
		return "", false
	}
	s, ok := val.(string)
	return s, ok
}

func (c *declarativeConfigMap) getBool(key string) (bool, bool) {
	if c == nil {
		return false, false
	}
	val, ok := (*c)[key]
	if !ok {
		return false, false
	}
	s, ok := val.(bool)
	return s, ok
}
