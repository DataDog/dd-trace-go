package stackparse

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestParse_PropertyBased(t *testing.T) {
	headers := []struct {
		Line    string
		WantG   *Goroutine
		WantErr string
	}{
		{
			Line:  "goroutine 1 [chan receive]:",
			WantG: &Goroutine{ID: 1, State: "chan receive", Waitduration: 0},
		},
		{
			Line:  "goroutine 23 [select, 5 minutes]:",
			WantG: &Goroutine{ID: 23, State: "select", Waitduration: 5 * time.Minute},
		},
		{
			Line:    "goroutine 23 []:",
			WantErr: "invalid goroutine header",
		},
		{
			Line:    "goroutine ",
			WantErr: "invalid goroutine header",
		},
	}

	funcs := []struct {
		Line      string
		WantFrame *Frame
		WantErr   string
	}{
		{
			Line:      "main.main()",
			WantFrame: &Frame{Func: "main.main"},
		},
		{
			Line:      "runtime.goparkunlock(...)",
			WantFrame: &Frame{Func: "runtime.goparkunlock"},
		},
		{
			Line:      "net/http.(*persistConn).writeLoop(0xc0001a5c20)",
			WantFrame: &Frame{Func: "net/http.(*persistConn).writeLoop"},
		},
		{
			Line:    "",
			WantErr: "invalid function call",
		},
		{
			Line:    "foo.bar",
			WantErr: "invalid function call",
		},
		{
			Line:    "foo.bar(",
			WantErr: "invalid function call",
		},
		{
			Line:    "net/http.(*persistConn).writeLoop(0xc0",
			WantErr: "invalid function call",
		},
		{
			Line:    "net/http.(*persist",
			WantErr: "invalid function call",
		},
		{
			Line:    "net/http.*persist)(",
			WantErr: "invalid function call",
		},
		{
			Line:    "net/http.(*persist))",
			WantErr: "invalid function call",
		},
		{
			Line:    "net/http.((*persist)",
			WantErr: "invalid function call",
		},
		{
			Line:    "()",
			WantErr: "invalid function call",
		},
	}

	files := []struct {
		Line      string
		WantFrame *Frame
		WantErr   string
	}{
		{
			Line:      "\t/go/src/example.org/example/main.go:231 +0x1187",
			WantFrame: &Frame{File: "/go/src/example.org/example/main.go", Line: 231},
		},
		{
			Line:      "\t/root/go1.15.6.linux.amd64/src/runtime/proc.go:312",
			WantFrame: &Frame{File: "/root/go1.15.6.linux.amd64/src/runtime/proc.go", Line: 312},
		},
		{
			Line:    "/root/go1.15.6.linux.amd64/src/runtime/proc.go:312",
			WantErr: "invalid file:line ref",
		},
		{
			Line:    "",
			WantErr: "invalid file:line ref",
		},
	}

	n := 0
	for _, header := range headers {
		for _, fn := range funcs {
			for _, file := range files {
				n++
				t.Run(fmt.Sprintf("%d", n), func(t *testing.T) {
					dump := header.Line + "\n" + fn.Line + "\n" + file.Line + "\n"
					goroutines, errs := Parse(strings.NewReader(dump))
					wantErrs := []string{header.WantErr, fn.WantErr, file.WantErr}

					for _, wantErr := range wantErrs {
						if wantErr != "" {
							require.NotNil(t, errs, dump)
							require.Equal(t, 1, len(errs.Errors), dump)
							require.Contains(t, errs.Errors[0].Error(), wantErr, dump)
							return
						}
					}

					require.Nil(t, errs, dump)

					require.Equal(t, 1, len(goroutines), dump)
					g := goroutines[0]
					require.Equal(t, header.WantG.ID, g.ID, dump)
					require.Equal(t, header.WantG.State, g.State, dump)
					require.Equal(t, header.WantG.Waitduration, g.Waitduration, dump)

					require.Equal(t, 1, len(g.Stack), dump)
					f := g.Stack[0]
					require.Equal(t, fn.WantFrame.Func, f.Func, dump)
					require.Equal(t, file.WantFrame.File, f.File, dump)
					require.Equal(t, file.WantFrame.Line, f.Line, dump)
				})
			}
		}
	}
}

func TestParse(t *testing.T) {
	t.Skip("broken")

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
