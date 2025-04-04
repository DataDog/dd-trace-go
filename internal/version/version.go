// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package version

import (
	"regexp"
	"strconv"
)

// Tag specifies the current release tag. It needs to be manually
// updated. A test checks that the value of Tag never points to a
// git tag that is older than HEAD.
const Tag = "v2.1.0-dev"

// Dissected version number. Filled during init()
var (
	// Major is the current major version number
	Major int
	// Minor is the current minor version number
	Minor int
	// Patch is the current patch version number
	Patch int
	// RC is the current release candidate version number
	RC int
)

func init() {
	Major, Minor, Patch, RC = parseVersion(Tag)
}

func parseVersion(version string) (int, int, int, int) {
	// This regexp matches the version format we use and captures major/minor/patch/rc in different groups
	r := regexp.MustCompile(`v(?P<ma>\d+)\.(?P<mi>\d+)\.(?P<pa>\d+)(-rc\.(?P<rc>\d+))?`)
	names := r.SubexpNames()
	captures := map[string]string{}
	// Associate each capture group match with the capture group's name to easily retrieve major/minor/patch/rc
	for k, v := range r.FindAllStringSubmatch(version, -1)[0] {
		captures[names[k]] = v
	}
	major, _ := strconv.Atoi(captures["ma"])
	minor, _ := strconv.Atoi(captures["mi"])
	patch, _ := strconv.Atoi(captures["pa"])
	rc, _ := strconv.Atoi(captures["rc"])
	return major, minor, patch, rc
}
