// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package version

import (
	"bytes"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestTag(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		// git probably not installed; don't fail the test suite
		t.Skip(err)
	}
	out, err := exec.Command("git", "show", "-s", "--format=%ct", "HEAD", Tag).CombinedOutput()
	if err != nil {
		if bytes.Contains(out, []byte("unknown revision")) {
			// test passed: the tag was not found
			return
		}
		t.Skip(err)
	}
	dates := strings.Split(string(bytes.TrimSpace(out)), "\n")
	if len(dates) != 2 {
		t.Skip("unexpected output: ", dates)
	}
	dateHEAD, err := unixDate(dates[0])
	if err != nil {
		t.Skip(err)
	}
	dateTag, err := unixDate(dates[1])
	if err != nil {
		t.Skip(err)
	}
	if dateTag.Before(dateHEAD) {
		t.Fatalf(
			"\n(internal/version).Tag value needs to be updated!\n• %s was already released %s\n• Latest commit (HEAD) dates %s",
			Tag,
			dateTag.Format(time.Stamp),
			dateHEAD.Format(time.Stamp),
		)
	}
}

func unixDate(u string) (time.Time, error) {
	sec, err := strconv.ParseInt(u, 10, 64)
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(sec, 0), nil
}
