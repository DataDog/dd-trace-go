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
	out, err := exec.Command("git", "show", "-s", "--format=%ct", "HEAD", Tag).CombinedOutput()
	if err != nil {
		if strings.Contains(err.Error(), "file not found") {
			t.Skip("git not installed")
		}
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
