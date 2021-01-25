package stackparse

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestParse_Example verifies parse by checking that it seems to parse a
// real-world example correctly.
func TestParse_Example(t *testing.T) {
	data, err := ioutil.ReadFile("example.txt")
	require.NoError(t, err)
	goroutines, err := Parse(bytes.NewReader(data))
	require.Nil(t, err)
	require.Len(t, goroutines, 10)
	g0 := goroutines[0]
	require.Equal(t, 1, g0.ID)
	require.Equal(t, "running", g0.State)
	require.Equal(t, time.Duration(0), g0.Wait)
	require.Equal(t, false, g0.LockedToThread)
	require.Len(t, g0.Stack, 6)
	require.Equal(t, "runtime/pprof.writeGoroutineStacks", g0.Stack[0].Func)
	require.Equal(t, "/usr/local/Cellar/go/1.15.6/libexec/src/runtime/pprof/pprof.go", g0.Stack[0].File)
	require.Equal(t, 693, g0.Stack[0].Line)
	require.Nil(t, g0.CreatedBy)

	// not checking g1..g7, they aren't very interesting

	g8 := goroutines[8]
	require.Equal(t, 7, g8.ID)
	require.Equal(t, true, g8.LockedToThread)

	g9 := goroutines[9]
	require.Equal(t, 41, g9.ID)
	require.Equal(t, "IO wait", g9.State)
	require.Equal(t, time.Minute, g9.Wait)
	require.Equal(t, false, g9.LockedToThread)
	require.Len(t, g9.Stack, 15)
	require.Equal(t, "internal/poll.runtime_pollWait", g9.Stack[0].Func)
	require.Equal(t, "/usr/local/Cellar/go/1.15.6/libexec/src/runtime/netpoll.go", g9.Stack[0].File)
	require.Equal(t, 222, g9.Stack[0].Line)
	require.Equal(t, "net/http.(*Server).Serve", g9.CreatedBy.Func)
	require.Equal(t, "/usr/local/Cellar/go/1.15.6/libexec/src/net/http/server.go", g9.CreatedBy.File)
	require.Equal(t, 2969, g9.CreatedBy.Line)
}

// TestParse_PropertyBased does an exhaustive property based test against all
// possible permutations of the line fragements defined below, making sure
// the parser always does the right thing and never panics.
func TestParse_PropertyBased(t *testing.T) {
	seen := map[string]bool{}
	tests := fixtures.Permutations()
	for i := 0; i < tests; i++ {
		dump := fixtures.Generate(i)
		dumpS := dump.String()
		msg := fmt.Sprintf("permutation %d:\n%s", i, dumpS)

		require.False(t, seen[dumpS], msg)
		seen[dumpS] = true

		goroutines, err := Parse(strings.NewReader(dumpS))

		wantErr := dump.header.WantErr
		for _, f := range dump.stack {
			if wantErr != "" {
				break
			} else if f.fn.WantErr != "" {
				wantErr = f.fn.WantErr
			} else if f.file.WantErr != "" {
				wantErr = f.file.WantErr
			}
		}
		if wantErr == "" {
			wantErr = dump.createdBy.fn.WantErr
		}
		if wantErr == "" && dump.createdBy.fn.WantFrame != nil {
			wantErr = dump.createdBy.file.WantErr
		}

		if wantErr != "" {
			require.NotNil(t, err, msg)
			require.Contains(t, err.Errors[0].Error(), wantErr, msg)
			require.Equal(t, 0, len(goroutines), msg)
			continue
		}

		require.Nil(t, err, msg)

		require.Equal(t, 1, len(goroutines), msg)
		g := goroutines[0]
		require.Equal(t, dump.header.WantG.ID, g.ID, msg)
		require.Equal(t, dump.header.WantG.State, g.State, msg)
		require.Equal(t, dump.header.WantG.Wait, g.Wait, msg)
		require.Equal(t, dump.header.WantG.LockedToThread, g.LockedToThread, msg)

		require.Equal(t, len(dump.stack), len(g.Stack), msg)
		for i, dumpFrame := range dump.stack {
			gFrame := g.Stack[i]
			require.Equal(t, dumpFrame.fn.WantFrame.Func, gFrame.Func, msg)
			require.Equal(t, dumpFrame.file.WantFrame.File, gFrame.File, msg)
			require.Equal(t, dumpFrame.file.WantFrame.Line, gFrame.Line, msg)
		}

		if dump.createdBy.fn.WantFrame == nil {
			require.Nil(t, g.CreatedBy, msg)
		} else {
			require.Equal(t, dump.createdBy.fn.WantFrame.Func, g.CreatedBy.Func, msg)
			require.Equal(t, dump.createdBy.file.WantFrame.File, g.CreatedBy.File, msg)
			require.Equal(t, dump.createdBy.file.WantFrame.Line, g.CreatedBy.Line, msg)
		}
	}
	t.Logf("executed %d tests", tests)
}

type dump struct {
	header    headerLine
	stack     []frameLines
	createdBy frameLines
}

func (d *dump) String() string {
	s := d.header.String()
	for _, f := range d.stack {
		s += f.String()
	}
	s += d.createdBy.String()
	return s
}

type headerLine struct {
	Line    string
	WantG   *Goroutine
	WantErr string
}

func (h *headerLine) String() string {
	return h.Line + "\n"
}

type frameLines struct {
	fn   frameLine
	file frameLine
}

func (f *frameLines) String() string {
	return f.fn.Line + "\n" + f.file.Line + "\n"
}

type frameLine struct {
	Line      string
	WantFrame *Frame
	WantErr   string
}

// generator generates all possible goroutine stack trace permutations based on
// the given stack depths and line fragements.
type generator struct {
	minStack  int
	maxStack  int
	headers   []headerLine
	funcs     []frameLine
	files     []frameLine
	createdBy []frameLine
}

func (g *generator) Generate(n int) *dump {
	// keep going around in circles
	n = n % g.Permutations()

	header := n % len(g.headers)
	n = n / len(g.headers)

	cFn := n % len(g.createdBy)
	n = n / len(g.createdBy)
	cFile := n % len(g.files)
	n = n / len(g.files)

	var stack []frameLines
	for d := 0; d < g.maxStack && (n > 0 || d < g.minStack); d++ {
		fn := n % len(g.funcs)
		n = n / len(g.funcs)
		file := n % len(g.files)
		n = n / len(g.files)
		frame := frameLines{
			fn:   g.funcs[fn],
			file: g.files[file],
		}
		stack = append(stack, frame)
	}

	d := &dump{
		header: g.headers[header],
		stack:  stack,
		createdBy: frameLines{
			fn:   g.createdBy[cFn],
			file: g.files[cFile],
		},
	}
	return d
}

func (g *generator) Permutations() int {
	p := 0
	for d := g.minStack; d <= g.maxStack; d++ {
		pp := 1
		for frame := 0; frame < d; frame++ {
			pp = pp * len(g.files) * len(g.funcs)
		}
		p += len(g.headers) * pp * len(g.createdBy) * len(g.files)
	}
	return p
}

var fixtures = generator{
	// Testing larger stack depths greatly increases the number of permutations
	// but is unlikely to shake out more bugs, so a depth of 1 to 2 seems like
	// the sweet spot.
	minStack: 1,
	maxStack: 2,

	headers: []headerLine{
		{
			Line:  "goroutine 1 [chan receive]:",
			WantG: &Goroutine{ID: 1, State: "chan receive", Wait: 0},
		},
		{
			Line:  "goroutine 2 [IO Wait, locked to thread]:",
			WantG: &Goroutine{ID: 2, State: "IO Wait", Wait: 0, LockedToThread: true},
		},
		{
			Line:  "goroutine 23 [select, 5 minutes]:",
			WantG: &Goroutine{ID: 23, State: "select", Wait: 5 * time.Minute},
		},
		{
			Line:  "goroutine 42 [select, 5 minutes, locked to thread]:",
			WantG: &Goroutine{ID: 42, State: "select", Wait: 5 * time.Minute, LockedToThread: true},
		},
		{
			Line:    "goroutine 23 []:",
			WantErr: "invalid goroutine header",
		},
		{
			Line:    "goroutine ",
			WantErr: "invalid goroutine header",
		},
		{
			Line:    "goroutine 1 [chan receive]:\n",
			WantErr: "invalid function call",
		},
	},

	funcs: []frameLine{
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
	},

	files: []frameLine{
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
	},

	createdBy: []frameLine{
		{
			Line:      "created by net/http.(*Server).Serve",
			WantFrame: &Frame{Func: "net/http.(*Server).Serve"},
		},
		{
			Line:      "created by github.com/example.org/example/k8s.io/klog.init.0",
			WantFrame: &Frame{Func: "github.com/example.org/example/k8s.io/klog.init.0"},
		},
	},
}

func BenchmarkParse(b *testing.B) {
	data, err := ioutil.ReadFile("example.txt")
	require.NoError(b, err)

	b.ResetTimer()

	start := time.Now()
	parsedBytes := 0
	for i := 0; i < b.N; i++ {
		parsedBytes += len(data)
		gs, err := Parse(bytes.NewReader(data))
		if err != nil {
			b.Fatal(err)
		} else if l := len(gs); l != 10 {
			b.Fatal(l)
		}
	}

	mbPerSec := float64(parsedBytes) / time.Since(start).Seconds() / 1024 / 1024
	b.ReportMetric(mbPerSec, "MB/s")
}

func TestFuzzCorupus(t *testing.T) {
	if os.Getenv("FUZZ_CORPUS") == "" {
		t.Skip("set FUZZ_CORPUS=true to generate fuzz corupus")
	}
	tests := fixtures.Permutations()
	for i := 0; i < tests; i++ {
		dump := fixtures.Generate(i)
		name := filepath.Join("corpus", fmt.Sprintf("%d.txt", i))
		err := ioutil.WriteFile(name, []byte(dump.String()), 0666)
		if err != nil {
			t.Fatal(err)
		}
	}
}
