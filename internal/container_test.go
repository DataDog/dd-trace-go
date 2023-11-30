// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package internal

import (
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadContainerID(t *testing.T) {
	for in, out := range map[string]string{
		`other_line
10:hugetlb:/kubepods/burstable/podfd52ef25-a87d-11e9-9423-0800271a638e/8c046cb0b72cd4c99f51b5591cd5b095967f58ee003710a45280c28ee1a9c7fa
9:cpuset:/kubepods/burstable/podfd52ef25-a87d-11e9-9423-0800271a638e/8c046cb0b72cd4c99f51b5591cd5b095967f58ee003710a45280c28ee1a9c7fa
8:pids:/kubepods/burstable/podfd52ef25-a87d-11e9-9423-0800271a638e/8c046cb0b72cd4c99f51b5591cd5b095967f58ee003710a45280c28ee1a9c7fa
7:freezer:/kubepods/burstable/podfd52ef25-a87d-11e9-9423-0800271a638e/8c046cb0b72cd4c99f51b5591cd5b095967f58ee003710a45280c28ee1a9c7fa
6:cpu,cpuacct:/kubepods/burstable/podfd52ef25-a87d-11e9-9423-0800271a638e/8c046cb0b72cd4c99f51b5591cd5b095967f58ee003710a45280c28ee1a9c7fa
5:perf_event:/kubepods/burstable/podfd52ef25-a87d-11e9-9423-0800271a638e/8c046cb0b72cd4c99f51b5591cd5b095967f58ee003710a45280c28ee1a9c7fa
4:blkio:/kubepods/burstable/podfd52ef25-a87d-11e9-9423-0800271a638e/8c046cb0b72cd4c99f51b5591cd5b095967f58ee003710a45280c28ee1a9c7fa
3:devices:/kubepods/burstable/podfd52ef25-a87d-11e9-9423-0800271a638e/8c046cb0b72cd4c99f51b5591cd5b095967f58ee003710a45280c28ee1a9c7fa
2:net_cls,net_prio:/kubepods/burstable/podfd52ef25-a87d-11e9-9423-0800271a638e/8c046cb0b72cd4c99f51b5591cd5b095967f58ee003710a45280c28ee1a9c7fa`: "8c046cb0b72cd4c99f51b5591cd5b095967f58ee003710a45280c28ee1a9c7fa",
		"10:hugetlb:/kubepods/burstable/podfd52ef25-a87d-11e9-9423-0800271a638e/8c046cb0b72cd4c99f51b5591cd5b095967f58ee003710a45280c28ee1a9c7fa": "8c046cb0b72cd4c99f51b5591cd5b095967f58ee003710a45280c28ee1a9c7fa",
		"10:hugetlb:/kubepods": "",
		"11:hugetlb:/ecs/55091c13-b8cf-4801-b527-f4601742204d/432624d2150b349fe35ba397284dea788c2bf66b885d14dfc1569b01890ca7da": "432624d2150b349fe35ba397284dea788c2bf66b885d14dfc1569b01890ca7da",
		"1:name=systemd:/docker/34dc0b5e626f2c5c4c5170e34b10e7654ce36f0fcd532739f4445baabea03376":                               "34dc0b5e626f2c5c4c5170e34b10e7654ce36f0fcd532739f4445baabea03376",
		"1:name=systemd:/uuid/34dc0b5e-626f-2c5c-4c51-70e34b10e765":                                                             "34dc0b5e-626f-2c5c-4c51-70e34b10e765",
		"1:name=systemd:/ecs/34dc0b5e626f2c5c4c5170e34b10e765-1234567890":                                                       "34dc0b5e626f2c5c4c5170e34b10e765-1234567890",
		"1:name=systemd:/docker/34dc0b5e626f2c5c4c5170e34b10e7654ce36f0fcd532739f4445baabea03376.scope":                         "34dc0b5e626f2c5c4c5170e34b10e7654ce36f0fcd532739f4445baabea03376",
		`1:name=systemd:/nope
2:pids:/docker/34dc0b5e626f2c5c4c5170e34b10e7654ce36f0fcd532739f4445baabea03376
3:cpu:/invalid`: "34dc0b5e626f2c5c4c5170e34b10e7654ce36f0fcd532739f4445baabea03376",
		`other_line
12:memory:/system.slice/garden.service/garden/6f265890-5165-7fab-6b52-18d1
11:rdma:/
10:freezer:/garden/6f265890-5165-7fab-6b52-18d1
9:hugetlb:/garden/6f265890-5165-7fab-6b52-18d1
8:pids:/system.slice/garden.service/garden/6f265890-5165-7fab-6b52-18d1
7:perf_event:/garden/6f265890-5165-7fab-6b52-18d1
6:cpu,cpuacct:/system.slice/garden.service/garden/6f265890-5165-7fab-6b52-18d1
5:net_cls,net_prio:/garden/6f265890-5165-7fab-6b52-18d1
4:cpuset:/garden/6f265890-5165-7fab-6b52-18d1
3:blkio:/system.slice/garden.service/garden/6f265890-5165-7fab-6b52-18d1
2:devices:/system.slice/garden.service/garden/6f265890-5165-7fab-6b52-18d1
1:name=systemd:/system.slice/garden.service/garden/6f265890-5165-7fab-6b52-18d1`: "6f265890-5165-7fab-6b52-18d1",
		"1:name=systemd:/system.slice/garden.service/garden/6f265890-5165-7fab-6b52-18d1": "6f265890-5165-7fab-6b52-18d1",
	} {
		id := parseContainerID(strings.NewReader(in))
		if id != out {
			t.Fatalf("%q -> %q: %q", in, out, id)
		}
	}
}

func TestReadContainerIDFromCgroup(t *testing.T) {
	cid := "8c046cb0b72cd4c99f51b5591cd5b095967f58ee003710a45280c28ee1a9c7fa"
	cgroupContents := "10:hugetlb:/kubepods/burstable/podfd52ef25-a87d-11e9-9423-0800271a638e/" + cid

	tmpFile, err := os.CreateTemp(os.TempDir(), "fake-cgroup-")
	if err != nil {
		t.Fatalf("failed to create fake cgroup file: %v", err)
	}
	defer os.Remove(tmpFile.Name())
	_, err = io.WriteString(tmpFile, cgroupContents)
	if err != nil {
		t.Fatalf("failed writing to fake cgroup file: %v", err)
	}
	err = tmpFile.Close()
	if err != nil {
		t.Fatalf("failed closing fake cgroup file: %v", err)
	}

	actualCID := readContainerID(tmpFile.Name())
	assert.Equal(t, cid, actualCID)
}

func TestPrioritizeContainerID(t *testing.T) {
	// reset cid after test
	defer func(cid string) { containerID = cid }(containerID)

	containerID = "fakeContainerID"
	eid := readEntityID()
	assert.Equal(t, "cid-fakeContainerID", eid)
}

func TestParseCgroupMountPath(t *testing.T) {
	// Test cases
	cases := []struct {
		name     string
		content  string
		expected string
	}{
		{
			name: "cgroup2 and cgroup mounts found",
			content: `
none /proc proc rw,nosuid,nodev,noexec,relatime 0 0
sysfs /sys sysfs rw,nosuid,nodev,noexec,relatime 0 0
cgroup2 /sys/fs/cgroup/cgroup2 cgroup2 rw,nosuid,nodev,noexec,relatime,cpuacct,cpu 0 0
tmpfs /sys/fs/cgroup tmpfs ro,nosuid,nodev,noexec,mode=755 0 0
cgroup /sys/fs/cgroup/cpu,cpuacct cgroup rw,nosuid,nodev,noexec,relatime,cpuacct,cpu 0 0
`,
			expected: "/sys/fs/cgroup/cgroup2",
		},
		{
			name: "only cgroup found",
			content: `
none /proc proc rw,nosuid,nodev,noexec,relatime 0 0
sysfs /sys sysfs rw,nosuid,nodev,noexec,relatime 0 0
tmpfs /sys/fs/cgroup tmpfs ro,nosuid,nodev,noexec,mode=755 0 0
cgroup /sys/fs/cgroup/cpu,cpuacct cgroup rw,nosuid,nodev,noexec,relatime,cpuacct,cpu 0 0
`,
			expected: "",
		},
		{
			name: "cgroup mount not found",
			content: `
none /proc proc rw,nosuid,nodev,noexec,relatime 0 0
sysfs /sys sysfs rw,nosuid,nodev,noexec,relatime 0 0
tmpfs /dev tmpfs rw,nosuid,size=65536k,mode=755 0 0
`,
			expected: "",
		},
	}

	// Run test cases
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			reader := strings.NewReader(c.content)
			result := parseCgroupV2MountPath(reader)
			require.Equal(t, c.expected, result)
		})
	}
}

func TestParseCgroupNodePath(t *testing.T) {
	// Test cases
	cases := []struct {
		name     string
		content  string
		expected string
	}{
		{
			name:     "cgroup2 normal case",
			content:  `0::/`,
			expected: "/",
		},
		{
			name: "cgroup node path found",
			content: `other_line
12:pids:/docker/abc123
11:hugetlb:/docker/abc123
10:net_cls,net_prio:/docker/abc123
0::/docker/abc123
`,
			expected: "/docker/abc123",
		},
		{
			name: "cgroup node path not found",
			content: `12:pids:/docker/abc123
11:hugetlb:/docker/abc123
10:net_cls,net_prio:/docker/abc123
`,
			expected: "",
		},
	}

	// Run test cases
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			reader := strings.NewReader(c.content)
			result := parseCgroupV2NodePath(reader)
			require.Equal(t, c.expected, result)
		})
	}
}

func TestGetCgroupInode(t *testing.T) {
	tests := []struct {
		description           string
		procMountsContent     string
		cgroupNodeDir         string
		procSelfCgroupContent string
		expectedResult        string
	}{
		{
			description:           "default case - matching entry in /proc/self/cgroup and /proc/mounts",
			procMountsContent:     "cgroup2 %s cgroup2 rw,nosuid,nodev,noexec,relatime,cpu,cpuacct 0 0\n",
			cgroupNodeDir:         "system.slice/docker-abcdef0123456789abcdef0123456789.scope",
			procSelfCgroupContent: "0::/system.slice/docker-abcdef0123456789abcdef0123456789.scope\n",
			expectedResult:        "in-%d", // Will be formatted with inode number
		},
		{
			description:           "should not match cgroup v1",
			procMountsContent:     "cgroup %s cgroup rw,nosuid,nodev,noexec,relatime,cpu,cpuacct 0 0\n",
			cgroupNodeDir:         "system.slice/docker-abcdef0123456789abcdef0123456789.scope",
			procSelfCgroupContent: "0::/system.slice/docker-abcdef0123456789abcdef0123456789.scope\n",
			expectedResult:        "",
		},
		{
			description: "hybrid cgroup - should match only cgroup2",
			procMountsContent: `other_line
cgroup /sys/fs/cgroup/memory cgroup foo,bar 0 0
cgroup2 %s cgroup2 rw,nosuid,nodev,noexec,relatime,cpu,cpuacct 0 0
`,
			cgroupNodeDir:         "system.slice/docker-abcdef0123456789abcdef0123456789.scope",
			procSelfCgroupContent: "0::/system.slice/docker-abcdef0123456789abcdef0123456789.scope\n",
			expectedResult:        "in-%d", // Will be formatted with inode number
		},
		{
			description:           "Non-matching entry in /proc/self/cgroup",
			procMountsContent:     "cgroup2 %s cgroup2 rw,nosuid,nodev,noexec,relatime,cpu,cpuacct 0 0\n",
			cgroupNodeDir:         "system.slice/nonmatching-scope.scope",
			procSelfCgroupContent: "0::/system.slice/docker-abcdef0123456789abcdef0123456789.scope\n",
			expectedResult:        "",
		},
		{
			description:           "No cgroup2 entry in /proc/mounts",
			procMountsContent:     "tmpfs %s tmpfs rw,nosuid,nodev,noexec,relatime 0 0\n",
			cgroupNodeDir:         "system.slice/docker-abcdef0123456789abcdef0123456789.scope",
			procSelfCgroupContent: "0::/system.slice/docker-abcdef0123456789abcdef0123456789.scope\n",
			expectedResult:        "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.description, func(t *testing.T) {
			// Setup
			cgroupMountPath, err := os.MkdirTemp(os.TempDir(), "sysfscgroup")
			require.NoError(t, err)
			defer os.RemoveAll(cgroupMountPath)

			cgroupNodePath := path.Join(cgroupMountPath, tc.cgroupNodeDir)
			err = os.MkdirAll(cgroupNodePath, 0755)
			require.NoError(t, err)
			defer os.RemoveAll(cgroupNodePath)

			stat, err := os.Stat(cgroupNodePath)
			require.NoError(t, err)

			expectedInode := ""
			if tc.expectedResult != "" {
				expectedInode = fmt.Sprintf(tc.expectedResult, stat.Sys().(*syscall.Stat_t).Ino)
			}

			procMounts, err := os.CreateTemp("", "procmounts")
			require.NoError(t, err)
			defer os.Remove(procMounts.Name())
			_, err = procMounts.WriteString(fmt.Sprintf(tc.procMountsContent, cgroupMountPath))
			require.NoError(t, err)
			err = procMounts.Close()
			require.NoError(t, err)

			procSelfCgroup, err := os.CreateTemp("", "procselfcgroup")
			require.NoError(t, err)
			defer os.Remove(procSelfCgroup.Name())
			_, err = procSelfCgroup.WriteString(tc.procSelfCgroupContent)
			require.NoError(t, err)
			err = procSelfCgroup.Close()
			require.NoError(t, err)

			// Test
			result := getCgroupV2Inode(procMounts.Name(), procSelfCgroup.Name())
			require.Equal(t, expectedInode, result)
		})
	}
}
