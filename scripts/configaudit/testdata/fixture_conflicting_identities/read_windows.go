//go:build windows

package fixtureconflictingidentities

import "os"

var readEnv = os.LookupEnv

func wrappedEnv(key string) (string, bool) {
	return os.LookupEnv(key)
}
