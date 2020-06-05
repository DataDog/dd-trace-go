// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2020 Datadog, Inc.

package internal

import (
	"bufio"
	"io"
	"os"
	"regexp"
)

var (
	// expLine matches a line in the /proc/self/cgroup file. It has a submatch for the last element (path), which contains the container ID.
	expLine = regexp.MustCompile(`^\d+:[^:]*:(.+)$`)
	// expContainerID matches contained IDs and sources. Source: https://github.com/Qard/container-info/blob/master/index.js
	expContainerID = regexp.MustCompile(`([0-9a-f]{8}[-_][0-9a-f]{4}[-_][0-9a-f]{4}[-_][0-9a-f]{4}[-_][0-9a-f]{12}|[0-9a-f]{64})(?:.scope)?$`)
	// containerID is the containerID read at init from /proc/self/cgroup
	containerID string
)

func init() {
	f, err := os.Open("/proc/self/cgroup")
	if err != nil {
		containerID = ""
		return
	}
	defer f.Close()
	containerID = readContainerID(f)
}

// readContainerID finds the first container ID reading from r and returns it.
func readContainerID(r io.Reader) string {
	scn := bufio.NewScanner(r)
	for scn.Scan() {
		path := expLine.FindStringSubmatch(scn.Text())
		if len(path) != 2 {
			// invalid entry, continue
			continue
		}
		if id := expContainerID.FindString(path[1]); id != "" {
			return id
		}
	}
	return ""
}

// ContainerID returns the container id read from /proc/self/cgroup on app init.
func ContainerID() string {
	return containerID
}
