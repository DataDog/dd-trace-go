package traceproftest

import (
	"bytes"
	"os"
	"runtime/pprof"
	"strings"
	"testing"
	"time"

	"github.com/google/pprof/profile"
	"github.com/stretchr/testify/require"
)

// StartCPUProfile starts a new CPU profile.
func StartCPUProfile(t testing.TB) *CPUProfiler {
	cp := &CPUProfiler{}
	cp.start(t)
	return cp
}

// CPUProfiler is a simplified implementation of the CPU profiler found in pkg
// profiler that retains essential performance characteristics but is more
// convenient for testing.
//
// TODO(fg) Would be nice to figure out a clean way to use the actual profiler
// pkg for this in the future.
type CPUProfiler struct {
	buf     bytes.Buffer
	prof    *CPUProfile
	stopped bool
}

// Stop stops the CPU profiler and returns the CPU profile.
func (c *CPUProfiler) Stop(t testing.TB) *CPUProfile {
	if c.stopped {
		return c.prof
	}
	c.stopped = true
	pprof.StopCPUProfile()
	var err error
	c.prof, err = NewCPUProfile(c.buf.Bytes())
	require.NoError(t, err)
	return c.prof
}

// start starts the cpu profiler.
func (c *CPUProfiler) start(t testing.TB) {
	require.NoError(t, pprof.StartCPUProfile(&c.buf))
}

// NewCPUProfile returns a new CPU profile for the given data.
func NewCPUProfile(data []byte) (*CPUProfile, error) {
	cp := &CPUProfile{data: data}
	prof, err := profile.ParseData(data)
	cp.prof = prof
	return cp, err
}

// CPUProfile is a test utility to extract data from a CPU profile for testing.
type CPUProfile struct {
	data []byte
	prof *profile.Profile
}

// Duration returns the total amont of CPU time in this profile.
func (c *CPUProfile) Duration() (d time.Duration) {
	return c.LabelsDuration(nil)
}

// LabelTime returns the CPU time for the given pprof label in this profile.
func (c *CPUProfile) LabelDuration(label, val string) (d time.Duration) {
	return c.LabelsDuration(map[string]string{label: val})
}

// LabelsDuration returns the CPU time for the given pprof labels in this
// profile.
func (c *CPUProfile) LabelsDuration(labels map[string]string) (d time.Duration) {
sampleloop:
	for _, s := range c.prof.Sample {
		for k, v := range labels {
			if vals := s.Label[k]; len(vals) != 1 || vals[0] != v {
				continue sampleloop
			}
		}
		d += time.Duration(s.Value[1])
	}
	return d
}

// Samples returns the number of samples in the CPU profile.
func (c *CPUProfile) Samples() int {
	return len(c.prof.Sample)
}

// Size returns the size of the pprof encoded CPU profile in bytes.
func (c *CPUProfile) Size() int {
	return len(c.data)
}

// Labels returns the number of samples per individual label in this profile.
func (c *CPUProfile) Labels() map[Label]int {
	m := map[Label]int{}
	for _, s := range c.prof.Sample {
		for k, v := range s.Label {
			val := strings.Join(v, ",")
			lbl := Label{Key: k, Val: val}
			m[lbl]++
		}
	}
	return m
}

// WriteFile writes the profile to the given path.
func (c *CPUProfile) WriteFile(path string) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return c.prof.Write(file)
}

// Label represents a simplified pprof label where the value is a
// comma-separated string rather than a []string.
type Label struct {
	Key string
	Val string
}
