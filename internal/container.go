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

	// cgroupV2Key is the key used to store the cgroup v2 identify the cgroupv2 mount point in the cgroupMounts map.
	cgroupV2Key = "cgroupv2"

	// cgroupV1BaseController is the base controller used to identify the cgroup v1 mount point in the cgroupMounts map.
	cgroupV1BaseController = "memory"

	// defaultCgroupMountPath is the path to the cgroup mount point.
	defaultCgroupMountPath = "/sys/fs/cgroup"
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

// parseCgroupNodePath parses /proc/self/cgroup and returns a map of controller to its associated cgroup node path.
func parseCgroupNodePath(r io.Reader) map[string]string {
	res := make(map[string]string)
	scn := bufio.NewScanner(r)
	for scn.Scan() {
		line := scn.Text()
		tokens := strings.Split(line, ":")
		if len(tokens) != 3 {
			continue
		}
		if tokens[1] == cgroupV1BaseController || tokens[1] == "" {
			res[tokens[1]] = tokens[2]
		}
	}
	return res
}

// getCgroupInode returns the cgroup controller inode if it exists otherwise an empty string.
// The inode is prefixed by "in-" and is used by the agent to retrieve the container ID.
// We first try to retrieve the cgroupv1 memory controller inode, if it fails we try to retrieve the cgroupv2 inode.
func getCgroupInode(cgroupMountPath, procSelfCgroupPath string) string {
	// Parse /proc/self/cgroup to retrieve the paths to the memory controller (cgroupv1) and the cgroup node (cgroupv2)
	f, err := os.Open(procSelfCgroupPath)
	if err != nil {
		return ""
	}
	defer f.Close()
	cgroupControllersPaths := parseCgroupNodePath(f)

	// Retrieve the cgroup inode from /sys/fs/cgroup+cgroupNodePath
	for _, controller := range []string{cgroupV1BaseController, ""} {
		cgroupNodePath, ok := cgroupControllersPaths[controller]
		if !ok {
			continue
		}
		inode := inodeForPath(path.Join(cgroupMountPath, controller, cgroupNodePath))
		if inode != "" {
			return inode
		}
	}
	return ""
}

// inodeForPath returns the inode for the provided path or empty on failure.
func inodeForPath(path string) string {
	fi, err := os.Stat(path)
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
	return getCgroupInode(mountsPath, cgroupPath)
}

// EntityID attempts to return the container ID or the cgroup v2 node inode if the container ID is not available.
// The cid is prefixed with `cid-` and the inode with `in-`.
func EntityID() string {
	return entityID
}
