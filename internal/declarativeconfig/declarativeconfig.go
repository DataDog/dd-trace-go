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
