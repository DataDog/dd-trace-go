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

// parseCgroupMountPath parses the cgroup controller mount path from /proc/mounts and returns the chosen controller.
// It selects the cgroupv1 "memory" controller mount path if it exists, otherwise it selects the cgroupv2 mount point.
// If cgroup mount points are not detected, it returns an empty string.
func parseCgroupMountPathAndController(r io.Reader) (string, string) {
	mountPoints, err := discoverCgroupMountPoints(r)
	if err != nil {
		return "", ""
	}
	if cgroupRoot, ok := mountPoints[cgroupV1BaseController]; ok {
		return cgroupRoot, cgroupV1BaseController
	}
	return mountPoints[cgroupV2Key], ""
}

// parseCgroupNodePath parses the cgroup controller path from /proc/self/cgroup
// It returns an empty string if cgroup v2 is not used
// In cgroupv2, only 0::<path> should exist. In cgroupv1, we should have 0::<path> and [1-9]:memory:<path>
// Refer to https://man7.org/linux/man-pages/man7/cgroups.7.html#top_of_page
func parseCgroupNodePath(r io.Reader, controller string) string {
	scn := bufio.NewScanner(r)
	for scn.Scan() {
		line := scn.Text()
		tokens := strings.Split(line, ":")
		if len(tokens) != 3 {
			continue
		}
		if tokens[1] != controller {
			continue
		}
		return tokens[2]
	}
	return ""
}

// getCgroupInode returns the cgroup controller inode if it exists otherwise an empty string.
// The inode is prefixed by "in-" and is used by the agent to retrieve the container ID.
func getCgroupInode(mountsPath, cgroupPath string) string {
	// Retrieve a cgroup mount point from /proc/mounts
	f, err := os.Open(mountsPath)
	if err != nil {
		return ""
	}
	defer f.Close()
	cgroupMountPath, controller := parseCgroupMountPathAndController(f)
	if cgroupMountPath == "" {
		return ""
	}
	// Parse /proc/self/cgroup to retrieve the cgroup node path
	f, err = os.Open(cgroupPath)
	if err != nil {
		return ""
	}
	defer f.Close()
	cgroupNodePath := parseCgroupNodePath(f, controller)
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

// discoverCgroupMountPoints returns a map of cgroup controllers to their mount points.
// ported from https://github.com/DataDog/datadog-agent/blob/38b4788d6f19b3660cb7310ff33c4d352a4993a9/pkg/util/cgroups/reader_detector.go#L22
func discoverCgroupMountPoints(r io.Reader) (map[string]string, error) {
	mountPointsv1 := make(map[string]string)
	var mountPointsv2 string

	s := bufio.NewScanner(r)
	for s.Scan() {
		line := s.Text()

		tokens := strings.Fields(line)
		if len(tokens) >= 3 {
			// Check if the filesystem type is 'cgroup' or 'cgroup2'
			fsType := tokens[2]
			if !strings.HasPrefix(fsType, "cgroup") {
				continue
			}

			cgroupPath := tokens[1]
			if fsType == "cgroup" {
				// Target can be comma-separate values like cpu,cpuacct
				tsp := strings.Split(path.Base(cgroupPath), ",")
				for _, target := range tsp {
					// In case multiple paths are mounted for a single controller, take the shortest one
					previousPath := mountPointsv1[target]
					if previousPath == "" || len(cgroupPath) < len(previousPath) {
						mountPointsv1[target] = cgroupPath
					}
				}
			} else if tokens[2] == "cgroup2" {
				mountPointsv2 = cgroupPath
			}
		}
	}

	if len(mountPointsv1) == 0 && mountPointsv2 != "" {
		return map[string]string{cgroupV2Key: mountPointsv2}, nil
	}

	return mountPointsv1, nil
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
