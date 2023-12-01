// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package internal

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path"
	"regexp"
	"strings"
	"syscall"
)

const (
	// cgroupPath is the path to the cgroup file where we can find the container id if one exists.
	cgroupPath = "/proc/self/cgroup"

	// mountsPath is the path to the mounts file where we can find the cgroup v2 mount point.
	mountsPath = "/proc/mounts"
)

const (
	uuidSource      = "[0-9a-f]{8}[-_][0-9a-f]{4}[-_][0-9a-f]{4}[-_][0-9a-f]{4}[-_][0-9a-f]{12}|[0-9a-f]{8}(?:-[0-9a-f]{4}){4}$"
	containerSource = "[0-9a-f]{64}"
	taskSource      = "[0-9a-f]{32}-\\d+"
)

var (
	// expLine matches a line in the /proc/self/cgroup file. It has a submatch for the last element (path), which contains the container ID.
	expLine = regexp.MustCompile(`^\d+:[^:]*:(.+)$`)

	// expContainerID matches contained IDs and sources. Source: https://github.com/Qard/container-info/blob/master/index.js
	expContainerID = regexp.MustCompile(fmt.Sprintf(`(%s|%s|%s)(?:.scope)?$`, uuidSource, containerSource, taskSource))

	// containerID is the containerID read at init from /proc/self/cgroup
	containerID string

	// entityID is the entityID to use for the container. It is the `cid-<containerID>` if the container id available,
	// otherwise the cgroup v2 node inode prefixed with `in-` or an empty string on cgroup v1 or incompatible OS.
	// It is retrieved by finding cgroup2 mounts in /proc/mounts, finding the cgroup v2 node path in /proc/self/cgroup and
	// calling stat on mountPath+nodePath to get the inode.
	entityID string
)

func init() {
	containerID = readContainerID(cgroupPath)
	entityID = readEntityID()
}

// parseContainerID finds the first container ID reading from r and returns it.
func parseContainerID(r io.Reader) string {
	scn := bufio.NewScanner(r)
	for scn.Scan() {
		path := expLine.FindStringSubmatch(scn.Text())
		if len(path) != 2 {
			// invalid entry, continue
			continue
		}
		if parts := expContainerID.FindStringSubmatch(path[1]); len(parts) == 2 {
			return parts[1]
		}
	}
	return ""
}

// readContainerID attempts to return the container ID from the provided file path or empty on failure.
func readContainerID(fpath string) string {
	f, err := os.Open(fpath)
	if err != nil {
		return ""
	}
	defer f.Close()
	return parseContainerID(f)
}

// ContainerID attempts to return the container ID from /proc/self/cgroup or empty on failure.
func ContainerID() string {
	return containerID
}

// parseCgroupV2MountPath parses the cgroup mount path from /proc/mounts
// It returns an empty string if cgroup v2 is not used
func parseCgroupV2MountPath(r io.Reader) string {
	scn := bufio.NewScanner(r)
	for scn.Scan() {
		line := scn.Text()
		// a correct line line should be formatted as `cgroup2 <path> cgroup2 rw,nosuid,nodev,noexec,relatime,nsdelegate 0 0`
		tokens := strings.Fields(line)
		if len(tokens) >= 3 {
			fsType := tokens[2]
			if fsType == "cgroup2" {
				return tokens[1]
			}
		}
	}
	return ""
}

// parseCgroupV2NodePath parses the cgroup node path from /proc/self/cgroup
// It returns an empty string if cgroup v2 is not used
// With respect to https://man7.org/linux/man-pages/man7/cgroups.7.html#top_of_page, in cgroupv2, only 0::<path> should exist
func parseCgroupV2NodePath(r io.Reader) string {
	scn := bufio.NewScanner(r)
	for scn.Scan() {
		line := scn.Text()
		// The cgroup node path is the last element of the line starting with "0::"
		if strings.HasPrefix(line, "0::") {
			return line[3:]
		}
	}
	return ""
}

// getCgroupV2Inode returns the cgroup v2 node inode if it exists otherwise an empty string.
// The inode is prefixed by "in-" and is used by the agent to retrieve the container ID.
func getCgroupV2Inode(mountsPath, cgroupPath string) string {
	// Retrieve a cgroup mount point from /proc/mounts
	f, err := os.Open(mountsPath)
	if err != nil {
		return ""
	}
	defer f.Close()
	cgroupMountPath := parseCgroupV2MountPath(f)
	if cgroupMountPath == "" {
		return ""
	}
	// Parse /proc/self/cgroup to retrieve the cgroup node path
	f, err = os.Open(cgroupPath)
	if err != nil {
		return ""
	}
	defer f.Close()
	cgroupNodePath := parseCgroupV2NodePath(f)
	if cgroupNodePath == "" {
		return ""
	}
	// Retrieve the cgroup inode from the cgroup mount and cgroup node path
	fi, err := os.Stat(path.Clean(cgroupMountPath + cgroupNodePath))
	if err != nil {
		return ""
	}
	stats, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return ""
	}
	return fmt.Sprintf("in-%d", stats.Ino)
}

// readEntityID attempts to return the cgroup v2 node inode or empty on failure.
func readEntityID() string {
	if containerID != "" {
		return "cid-" + containerID
	}
	return getCgroupV2Inode(mountsPath, cgroupPath)
}

// EntityID attempts to return the container ID or the cgroup v2 node inode if the container ID is not available.
// The cid is prefixed with `cid-` and the inode with `in-`.
func EntityID() string {
	return entityID
}
