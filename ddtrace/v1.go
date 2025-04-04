package ddtrace

import (
	"runtime/debug"

	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/Masterminds/semver/v3"
)

func init() {
	detectV1NonTransitional()
}

func detectV1NonTransitional() {
	info, _ := debug.ReadBuildInfo()
	var v1Version string
	for _, dep := range info.Deps {
		if dep.Path == "gopkg.in/DataDog/dd-trace-go.v1" {
			v1Version = dep.Version
		}
	}
	if v1Version == "" {
		return
	}
	v := semver.MustParse(v1Version)
	if v.Major() > 1 {
		// Not possible but just in case
		return
	}
	if v.Minor() > 74 {
		// v1.74.0 is the first version that is transitional, so it can be used with v2
		return
	}
	log.Warn("Detected %q version of dd-trace-go, this version is not compatible with v2 - please upgrade to v1.74.0 or later", v1Version)
}
