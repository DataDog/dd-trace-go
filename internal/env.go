package internal

import (
	"os"
	"strconv"
)

// BoolEnv returns the parsed boolean value of an environment variable, or
// def if it fails to parse.
func BoolEnv(key string, def bool) bool {
	v, err := strconv.ParseBool(os.Getenv(key))
	if err != nil {
		return def
	}
	return v
}
