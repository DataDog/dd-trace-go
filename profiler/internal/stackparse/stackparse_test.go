package stackparse

import (
	"bytes"
	"io/ioutil"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestParse(t *testing.T) {
	var (
		mainT = `
goroutine 1 [chan receive, 6883 minutes]:
main.main()
	/go/src/example.org/example/main.go:231 +0x1187
	`
		mainG = &Goroutine{
			ID:           1,
			State:        "chan receive",
			Waitduration: 6883 * time.Minute,
			Stack: []*Frame{{
				Func: "main.main",
				File: "/go/src/example.org/example/main.go",
				Line: 231,
			}},
			CreatedBy: nil,
		}

		garbageT = "garbage\n"

		createdByT = `
goroutine 14 [chan receive]:
example.org/example/vendor/k8s.io/klog.(*loggingT).flushDaemon(0x3e18220)
	/go/src/example.org/example/vendor/k8s.io/klog/klog.go:941 +0x8b
created by example.org/example/vendor/k8s.io/klog.init.0
	/go/src/example.org/example/vendor/k8s.io/klog/klog.go:403 +0x6c
	`

		createdByG = &Goroutine{
			ID:           14,
			State:        "chan receive",
			Waitduration: 0,
			Stack: []*Frame{{
				Func: "example.org/example/vendor/k8s.io/klog.(*loggingT).flushDaemon",
				File: "/go/src/example.org/example/vendor/k8s.io/klog/klog.go",
				Line: 941,
			}},
			CreatedBy: &Frame{
				Func: "example.org/example/vendor/k8s.io/klog.init.0",
				File: "/go/src/example.org/example/vendor/k8s.io/klog/klog.go",
				Line: 403,
			},
		}

		badHeaderStateT = `
goroutine 33
example.org/example/vendor/k8s.io/klog.(*loggin
	/go/src/example.org/example/vendor/k8s.io/klog/klog.go:941 +0x8b
	`

		badHeaderIDT = `
goroutine abc
example.org/example/vendor/k8s.io/klog.(*loggingT).flushDaemon(0x3e18220)
	/go/src/example.org/example/vendor/k8s.io/klog/klog.go:941 +0x8b
	`

		badHeaderWaitMinutesT = `
goroutine 33 [chan receive, abc minutes]:
example.org/example/vendor/k8s.io/klog.(*loggingT).flushDaemon(0x3e18220)
	/go/src/example.org/example/vendor/k8s.io/klog/klog.go:941 +0x8b
	`

		badFuncT = `
goroutine 33 [chan receive]:
example.org/example/vendor/k8s.io/klog.(*loggi
	/go/src/example.org/example/vendor/k8s.io/klog/klog.go:941 +0x8b
	`

		badLineNumT = `
goroutine 33 [chan receive]:
example.org/example/vendor/k8s.io/klog.(*loggingT).flushDaemon(0x3e18220)
	/go/src/example.org/example/vendor/k8s.io/klog/klog.go:abc +0x8b
	`

		badFilePrefixT = `
goroutine 33 [chan receive]:
example.org/example/vendor/k8s.io/klog.(*loggingT).flushDaemon(0x3e18220)
/go/src/example.org/example/vendor/k8s.io/klog/klog.go:123 +0x8b
	`

		badFileColonsT = `
goroutine 33 [chan receive]:
example.org/example/vendor/k8s.io/klog.(*loggingT).flushDaemon(0x3e18220)
	/go/src/example.org/example/vendor/k8s.io/klog/klog.go:12:123 +0x8b
	`

		badFileNameT = `
goroutine 33 [chan receive]:
example.org/example/vendor/k8s.io/klog.(*loggingT).flushDaemon(0x3e18220)
	:123
	`

		badFileName2T = `
goroutine 33 [chan receive]:
example.org/example/vendor/k8s.io/klog.(*loggingT).flushDaemon(0x3e18220)
	
	`
	)

	t.Run("main", func(t *testing.T) {
		gs, err := Parse(strings.NewReader(mainT))
		require.Nil(t, err)
		require.Equal(t, []*Goroutine{mainG}, gs)
	})

	t.Run("garbage main garbage", func(t *testing.T) {
		gs, err := Parse(strings.NewReader(garbageT + mainT + garbageT))
		require.Nil(t, err)
		require.Equal(t, []*Goroutine{mainG}, gs)
	})

	t.Run("createdBy", func(t *testing.T) {
		gs, err := Parse(strings.NewReader(createdByT))
		require.Nil(t, err)
		require.Equal(t, []*Goroutine{createdByG}, gs)
	})

	t.Run("main createdBy", func(t *testing.T) {
		gs, err := Parse(strings.NewReader(mainT + createdByT))
		require.Nil(t, err)
		require.Equal(t, []*Goroutine{mainG, createdByG}, gs)
	})

	t.Run("badHeaderState", func(t *testing.T) {
		gs, err := Parse(strings.NewReader(badHeaderStateT))
		require.Nil(t, err)
		require.Nil(t, gs)
	})

	t.Run("badHeaderID", func(t *testing.T) {
		gs, err := Parse(strings.NewReader(badHeaderIDT))
		require.Nil(t, err)
		require.Nil(t, gs)
	})

	t.Run("badHeaderWaitMinutesT", func(t *testing.T) {
		gs, err := Parse(strings.NewReader(badHeaderWaitMinutesT))
		require.Nil(t, err)
		require.Nil(t, gs)
	})

	t.Run("badFunc", func(t *testing.T) {
		gs, err := Parse(strings.NewReader(badFuncT))
		require.Equal(t, 1, len(err.Errors))
		require.Contains(t, err.Errors[0].Error(), "expected function call")
		require.Equal(t, []*Goroutine{}, gs)
	})

	t.Run("main badFunc createdBy", func(t *testing.T) {
		gs, err := Parse(strings.NewReader(mainT + badFuncT + createdByT))
		require.Equal(t, 1, len(err.Errors))
		require.Equal(t, []*Goroutine{mainG, createdByG}, gs)
	})

	t.Run("badLineNum", func(t *testing.T) {
		gs, err := Parse(strings.NewReader(badLineNumT))
		require.Equal(t, 1, len(err.Errors))
		require.Contains(t, err.Errors[0].Error(), "expected file:line ref")
		require.Equal(t, []*Goroutine{}, gs)
	})

	t.Run("badFilePrefix", func(t *testing.T) {
		gs, err := Parse(strings.NewReader(badFilePrefixT))
		require.Equal(t, 1, len(err.Errors))
		require.Contains(t, err.Errors[0].Error(), "expected file:line ref")
		require.Equal(t, []*Goroutine{}, gs)
	})

	t.Run("badFileColons", func(t *testing.T) {
		gs, err := Parse(strings.NewReader(badFileColonsT))
		require.Equal(t, 1, len(err.Errors))
		require.Contains(t, err.Errors[0].Error(), "expected file:line ref")
		require.Equal(t, []*Goroutine{}, gs)
	})

	t.Run("badFileName", func(t *testing.T) {
		gs, err := Parse(strings.NewReader(badFileNameT))
		require.Equal(t, 1, len(err.Errors))
		require.Contains(t, err.Errors[0].Error(), "expected file:line ref")
		require.Equal(t, []*Goroutine{}, gs)
	})

	t.Run("badFileName2", func(t *testing.T) {
		gs, err := Parse(strings.NewReader(badFileName2T))
		require.Equal(t, 1, len(err.Errors))
		require.Contains(t, err.Errors[0].Error(), "expected file:line ref")
		require.Equal(t, []*Goroutine{}, gs)
	})
}

func BenchmarkParse(b *testing.B) {
	data, err := ioutil.ReadFile("paul.txt")
	require.NoError(b, err)

	b.ResetTimer()

	start := time.Now()
	parsedBytes := 0
	for i := 0; i < b.N; i++ {
		parsedBytes += len(data)
		_, err := Parse(bytes.NewReader(data))
		if err != nil {
			b.Fatal(err)
		}
	}

	mbPerSec := float64(parsedBytes) / time.Since(start).Seconds() / 1024 / 1024
	b.ReportMetric(mbPerSec, "MB/s")
}
