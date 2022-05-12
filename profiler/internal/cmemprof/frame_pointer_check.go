//go:build go1.18

// Go 1.18 adds the ability to query build settings, including CGO_CFLAGS, for
// Go executables. We can use this to check if -fno-omit-frame-pointer was set
// when compiling the program, which gives us some assurance that it's safe to
// unwind certain call stacks using frame pointer unwinding. The benefit of this
// is that we can still offload most unwinding to an external library and keep
// only very basic unwinding code in this package.
//
// Note: as of Go 1.18.1, test executables do NOT include this information. See
// https://github.com/golang/go/issues/33976

package cmemprof

// extern void cgo_heap_profiler_mark_fpunwind_safe(void);
import "C"
import (
	"runtime/debug"
	"strings"
)

func init() {
	build, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}

	for _, setting := range build.Settings {
		if setting.Key == "CGO_CFLAGS" && strings.Contains(setting.Value, "-fno-omit-frame-pointer") {
			C.cgo_heap_profiler_mark_fpunwind_safe()
		}
	}
}
