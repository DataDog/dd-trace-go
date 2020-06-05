// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2020 Datadog, Inc.

package internal

import (
	"io"
	"io/ioutil"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
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
	} {
		id := readContainerID(strings.NewReader(in))
		if id != out {
			t.Fatalf("%q -> %q", in, out)
		}
	}
}

func TestContainerIDCached(t *testing.T) {
	defer func(origCgroupCID *string) { cgroupCID = origCgroupCID }(cgroupCID)

	cid := "8c046cb0b72cd4c99f51b5591cd5b095967f58ee003710a45280c28ee1a9c7fa"
	testOverrideCgroup(t, "10:hugetlb:/kubepods/burstable/podfd52ef25-a87d-11e9-9423-0800271a638e/"+cid)
	actualCID := ContainerID()
	assert.Equal(t, cid, actualCID)

	// Now we change the cgroup file contents. It shouldn't be read again since previous execution should be cached.
	testOverrideCgroup(t, "10:hugetlb:/kubepods")
	actualCID = ContainerID()
	assert.Equal(t, cid, actualCID)
}

func testOverrideCgroup(t *testing.T, in string) func() {
	origCgroupPath := cgroupPath

	tmpFile, err := ioutil.TempFile(os.TempDir(), "fake-cgroup-")
	if err != nil {
		t.Fatalf("failed to create fake cgroup file: %v", err)
	}
	cgroupPath = tmpFile.Name()
	_, err = io.WriteString(tmpFile, in)
	if err != nil {
		t.Fatalf("failed writing to fake cgroup file: %v", err)
	}
	err = tmpFile.Close()
	if err != nil {
		t.Fatalf("failed closing fake cgroup file: %v", err)
	}

	return func() {
		os.Remove(tmpFile.Name())
		cgroupPath = origCgroupPath
	}
}
