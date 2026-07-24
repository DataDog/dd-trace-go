//go:build linux

package fixtureconflictingidentities

import "os"

var readEnv = os.Getenv

func wrappedEnv(key string) string {
	return os.Getenv(key)
}
