// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package crashtracker

import (
	"bytes"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/DataDog/gostackparse"

	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/osinfo"
)

// stackFormat is the stack format identifier reported to Error Tracking.
const stackFormat = "Datadog Crashtracker 1.0"

// ddSource is the ddsource value for crash reports.
const ddSource = "crashtracker"

var (
	// goroutineHeaderRe matches a goroutine block header in every format the
	// runtime prints. It only finds where the preamble ends: gostackparse
	// parses the blocks. Label values are quoted and can contain "]", so the
	// bracket and label parts match greedily.
	//   standard: "goroutine 1 [running]:"
	//   system:   "goroutine 1 gp=0xc000... m=0 [running]:"
	//   go1.26 labels: `goroutine 1 [running labels:{"k": "v"}]:`
	//   go1.27 labels: "goroutine 1 [running] {k: v}:"
	goroutineHeaderRe = regexp.MustCompile(`^goroutine \d+ [^\[]*\[.*\](?: \{.*\})?:$`)

	// The following expressions extract fields from the runtime signal line,
	// e.g. "[signal SIGSEGV: segmentation violation code=0x2 addr=0x0 pc=0x1000a79b4]".
	signalNameRe = regexp.MustCompile(`signal (SIG[A-Z0-9]+)`)
	signalCodeRe = regexp.MustCompile(`code=(0x[0-9a-fA-F]+|\d+)`)
	signalAddrRe = regexp.MustCompile(`addr=(0x[0-9a-fA-F]+)`)

	// topLevelSignalRe matches the Go runtime's top-level signal crash header,
	// e.g. "SIGABRT: abort" or "SIGSEGV: segmentation violation".
	// This format appears when the process is killed directly by a signal (not
	// wrapped in a panic), in contrast to the bracketed "[signal SIG…]" form.
	topLevelSignalRe = regexp.MustCompile(`^(SIG[A-Z0-9]+): `)

	// windowsExceptionRe matches the Go runtime's crash header for an
	// unhandled Windows structured exception, e.g.
	// "Exception 0xc0000005 0x0 0x18 0x7ff6a2345678" — runtime/signal_windows.go's
	// winthrow prints ExceptionCode, ExceptionInformation[0],
	// ExceptionInformation[1], then the faulting PC as four hex fields.
	// There is no Unix-style signal number here, so this crash kind is
	// classified without populating SigInfo, which is inherently POSIX-shaped.
	windowsExceptionRe = regexp.MustCompile(`^Exception (0x[0-9a-fA-F]+)`)
)

// maxReportThreads bounds how many goroutines a single report includes. A
// crash dump can contain thousands of goroutines; including all of them
// amplifies the parsed JSON well past the raw dump size (each frame becomes
// several JSON fields) with no cap, no compression until upload, and no
// signal that anything was dropped. The crashed goroutine is always kept.
const maxReportThreads = 100

// maxFramesPerThread bounds how many frames a single goroutine's stack
// contributes to a report. maxReportThreads bounds goroutine count, not depth:
// a single deeply recursive stack (a stack-overflow crash is exactly this) can
// still produce many thousands of frames in one goroutine, which
// maxReportThreads does nothing to stop.
const maxFramesPerThread = 1000

// parseCrashDump parses a raw Go crash dump into a Report.
// The input is the full text written by the Go runtime to the crash output fd.
func parseCrashDump(dump []byte) *Report {
	preamble, threads := splitDump(dump)

	sigInfo := parseSignal(preamble)
	errType := errorType(preamble, sigInfo)
	message := errorMessage(preamble, sigInfo)

	var stack *StackTrace
	var threadName string
	for i := range threads {
		if threads[i].Crashed {
			// error.stack intentionally repeats the crashed goroutine's frames
			// already present in error.threads: the errorsintake schema requires
			// error.stack as a distinct top-level field (see RFC 0011), so this is
			// not redundant to trim away. Note this is a shallow copy — both views
			// share one Frames backing array, so any future in-place rewriting of
			// frames (scrubbing, path trimming) would affect both.
			s := threads[i].Stack
			stack = &s
			threadName = threads[i].Name
			break
		}
	}

	return &Report{
		Timestamp: time.Now().UnixMilli(),
		DDSource:  ddSource,
		Error: Error{
			Type:       errType,
			Message:    message,
			Stack:      stack,
			Threads:    threads,
			ThreadName: threadName,
			IsCrash:    true,
			SourceType: "Crashtracking",
		},
		OSInfo:  osInfo(),
		SigInfo: sigInfo,
	}
}

// splitDump separates the leading message lines (preamble) from the goroutine
// stack blocks. The split point is the first goroutine header line. It returns
// at most maxReportThreads threads, always including the crashed one, and
// builds frames only for those.
//
// Note this also discards the frames of a leading "runtime stack:" block
// (emitted for stack overflow and for faults during runtime execution), since
// those precede any goroutine header. That is deliberate: the user goroutine
// below is the actionable stack for grouping and display.
func splitDump(dump []byte) (preamble []string, threads []Thread) {
	off := 0
	for off < len(dump) {
		end := len(dump)
		if i := bytes.IndexByte(dump[off:], '\n'); i >= 0 {
			end = off + i
		}
		raw := dump[off:end]
		if len(raw) >= maxCrashDumpSize {
			// A line this long cannot be a message line of a real dump. Mark the
			// output incomplete so callers know the parse is partial rather than
			// silently returning what looks like a complete result.
			return preamble, []Thread{{
				Crashed: true,
				Name:    "goroutine unknown",
				State:   "unknown",
				Stack:   StackTrace{Format: stackFormat, Incomplete: true},
			}}
		}
		line := bytes.TrimSuffix(raw, []byte("\r"))
		if goroutineHeaderRe.Match(line) {
			break
		}
		preamble = append(preamble, string(line))
		off = end + 1
	}
	if off >= len(dump) {
		return preamble, nil
	}

	// The returned errors are not used. Goroutine.Incomplete marks a goroutine
	// that was cut. A goroutine with an invalid header is dropped, and a line
	// over MaxLineSize ends the parse; neither can mark a goroutine.
	// MaxLineSize keeps lines up to the dump cap parseable.
	goroutines, _ := gostackparse.ParseWithOptions(bytes.NewReader(dump[off:]), gostackparse.ParseOptions{
		KeepIncomplete: true,
		MaxLineSize:    maxCrashDumpSize,
	})
	return preamble, reportThreads(goroutines)
}

// reportThreads converts the goroutines to report threads and marks the
// crashed one. A dump can hold thousands of goroutines, so the cap applies
// before the conversion and frames are built only for the kept ones.
func reportThreads(goroutines []*gostackparse.Goroutine) []Thread {
	crashed := crashingThread(goroutines)
	kept := goroutines
	if len(goroutines) > maxReportThreads {
		kept = make([]*gostackparse.Goroutine, 0, maxReportThreads)
		kept = append(kept, crashed)
		for _, g := range goroutines {
			if len(kept) >= maxReportThreads {
				break
			}
			if g != crashed {
				kept = append(kept, g)
			}
		}
		log.Warn("crashtracker: report truncated from %d to %d goroutines", len(goroutines), len(kept)) //errtrack:ignore expected payload capacity limit
	}
	threads := make([]Thread, len(kept))
	for i, g := range kept {
		threads[i] = newThread(g)
		threads[i].Crashed = g == crashed
	}
	return threads
}

// newThread converts a parsed goroutine into a Thread. The state keeps the
// runtime's wire format, e.g. "chan receive, 3 minutes, locked to thread".
// The created-by frame and the ancestors are not part of the thread's stack.
func newThread(g *gostackparse.Goroutine) Thread {
	state := g.State
	if g.Wait > 0 {
		state += ", " + strconv.Itoa(int(g.Wait/time.Minute)) + " minutes"
	}
	if g.LockedToThread {
		state += ", locked to thread"
	}
	t := Thread{
		Name:  "goroutine " + strconv.Itoa(g.ID),
		State: state,
		Stack: StackTrace{
			Format: stackFormat,
			// An elided stack, a stack over the frame cap and a goroutine
			// without frames are all partial.
			Incomplete: g.Incomplete || g.FramesElided || len(g.Stack) == 0 || len(g.Stack) > maxFramesPerThread,
		},
	}
	if n := min(len(g.Stack), maxFramesPerThread); n > 0 {
		t.Stack.Frames = make([]Frame, n)
		for i, f := range g.Stack[:n] {
			t.Stack.Frames[i] = Frame{Function: f.Func, File: f.File, Line: f.Line}
		}
	}
	return t
}

// crashingThread returns the goroutine that crashed: the first goroutine in
// the "running" state, or the first goroutine overall if none is running.
//
// That fallback is a best-effort attribution, not a positively identified
// crash location: an operator-triggered SIGQUIT can produce a dump with no
// "running" goroutine at all (SIGQUIT is a deliberate diagnostic dump
// request, not a fault inside a specific goroutine's execution, so a process
// that was otherwise idle when it arrived can have every goroutine parked in
// some other state). In that case dump order, not evidence of a fault,
// decides which goroutine error.stack and error.thread_name describe. This
// is deliberately still preferred over reporting no primary stack at all —
// an approximate crash location is more actionable than none — but it means
// error.stack should not be read as proof that goroutine actually caused the
// crash for a SIGQUIT-shaped report specifically.
func crashingThread(goroutines []*gostackparse.Goroutine) *gostackparse.Goroutine {
	if len(goroutines) == 0 {
		return nil
	}
	for _, g := range goroutines {
		if g.State == "running" {
			return g
		}
	}
	return goroutines[0]
}

// parseSignal extracts UNIX signal details from the preamble, or returns nil
// if the crash was not signal-triggered. It recognises two formats emitted by
// the Go runtime:
//
//   - Bracketed panic signal: "[signal SIGSEGV: segmentation violation code=… addr=… pc=…]"
//     — appears when a signal interrupts a running goroutine.
//   - Top-level fatal signal: "SIGABRT: abort" or "SIGSEGV: segmentation violation"
//     — appears when the process is killed directly by a signal with no
//     surrounding panic context.
func parseSignal(preamble []string) *SigInfo {
	for _, line := range preamble {
		// Bracketed form: "[signal SIG…]". Anchored to the runtime's actual
		// bracket rather than an unanchored substring test: a panic value that
		// happens to contain the text "signal SIG..." (e.g. a message like
		// "aborted: received signal SIGTERM before drain") would otherwise
		// misclassify as that signal.
		if strings.HasPrefix(strings.TrimSpace(line), "[signal ") {
			nameMatch := signalNameRe.FindStringSubmatch(line)
			if nameMatch == nil {
				continue
			}
			name := nameMatch[1]
			info := &SigInfo{
				SiSignoHuman: name,
				SiSigno:      signalNumbers[name],
			}
			if m := signalCodeRe.FindStringSubmatch(line); m != nil {
				info.SiCode = parseIntFlexible(m[1])
			}
			if m := signalAddrRe.FindStringSubmatch(line); m != nil {
				info.SiAddr = m[1]
			}
			return info
		}
		// Top-level form: "SIGABRT: abort"
		if m := topLevelSignalRe.FindStringSubmatch(line); m != nil {
			name := m[1]
			return &SigInfo{
				SiSignoHuman: name,
				SiSigno:      signalNumbers[name],
			}
		}
	}
	return nil
}

// errorType classifies the crash into an error.type value following the
// Crashtracking model. Anything that is not a signal or a recognised
// "fatal error:" kind is reported as a panic — this includes both
// explicit "panic:"/"panic(" preamble lines and the default case, since a
// panic is the only remaining crash kind Go's runtime produces.
func errorType(preamble []string, sigInfo *SigInfo) string {
	// A signal-triggered crash reports the specific signal name (e.g.
	// "SIGSEGV"), not the generic "UnixSignal" kind label, so Error Tracking
	// groups by the actual signal rather than lumping every signal together.
	if sigInfo != nil {
		return sigInfo.SiSignoHuman
	}
	for _, line := range preamble {
		if rest, ok := strings.CutPrefix(line, "fatal error:"); ok {
			return fatalErrorType(strings.TrimSpace(rest))
		}
		if windowsExceptionRe.MatchString(line) {
			// A native Windows exception (access violation, breakpoint, ...)
			// matches neither the Unix signal parser nor "fatal error:"; without
			// this it would reach the panic default below and misclassify
			// every native crash on Windows as a Go-level panic.
			return "WindowsException"
		}
	}
	return "panic"
}

// fatalErrorType maps a "fatal error: <msg>" message to its Go runtime error
// kind. Most fatal errors raised via throw surface as runtime.plainError.
func fatalErrorType(msg string) string {
	if msg == "" {
		return "runtime.Error"
	}
	return "runtime.plainError"
}

// errorMessage derives the human-readable error message from the preamble.
func errorMessage(preamble []string, sigInfo *SigInfo) string {
	// For signal crashes, prefer the "[signal SIG...]" line. Anchored the same
	// way as parseSignal, for the same reason.
	if sigInfo != nil {
		for _, line := range preamble {
			if strings.HasPrefix(strings.TrimSpace(line), "[signal ") {
				return strings.Trim(strings.TrimSpace(line), "[]")
			}
		}
	}
	for i, line := range preamble {
		if rest, ok := strings.CutPrefix(line, "fatal error:"); ok {
			return strings.TrimSpace(rest)
		}
		if strings.HasPrefix(line, "panic:") {
			return collectPanicMessage(preamble, i)
		}
		// A "panic(<args>)" line is a stack frame, not a preamble line, so this
		// branch is defensive only: in a dump the Go runtime produced, such a
		// line always follows a goroutine header and is therefore parsed as a
		// frame rather than reaching errorMessage.
		if strings.HasPrefix(line, "panic(") {
			return panicValue(line)
		}
	}
	// Fall back to the first non-empty preamble line.
	for _, line := range preamble {
		if s := strings.TrimSpace(line); s != "" {
			return s
		}
	}
	return ""
}

// collectPanicMessage returns a "panic:" line's message extended with any
// subsequent preamble lines up to the next blank line. A single "panic:" line
// is not always the whole message: a panic value containing an embedded
// newline prints as multiple physical lines, and a panic recovered and
// re-raised during deferred cleanup prints as an indented
// "panic: ... [recovered]" line followed by the final panic on its own line.
// Returning only the first line loses that continuation — including, in the
// recovered case, the panic that actually terminated the process.
func collectPanicMessage(preamble []string, start int) string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(strings.TrimPrefix(preamble[start], "panic:")))
	for _, line := range preamble[start+1:] {
		if strings.TrimSpace(line) == "" {
			break
		}
		b.WriteByte('\n')
		b.WriteString(strings.TrimSpace(line))
	}
	return b.String()
}

// panicValue extracts a simple string argument from a "panic(...)" frame line,
// e.g. panic("boom") -> boom. When the argument is not a simple quoted string
// (e.g. a pointer tuple), the whole line is returned unchanged.
func panicValue(line string) string {
	openIdx := strings.IndexByte(line, '(')
	closeIdx := strings.LastIndexByte(line, ')')
	if openIdx < 0 || closeIdx < 0 || closeIdx <= openIdx+1 {
		return strings.TrimSpace(line)
	}
	arg := strings.TrimSpace(line[openIdx+1 : closeIdx])
	if unquoted, err := strconv.Unquote(arg); err == nil {
		return unquoted
	}
	return strings.TrimSpace(line)
}

// parseIntFlexible parses an integer that may be expressed in hex (0x...) or
// decimal. Unparseable input yields 0.
func parseIntFlexible(s string) int {
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
		// Use bitSize 0 so ParseInt bounds the result to the native int width,
		// making the int(v) conversion safe on both 32- and 64-bit platforms.
		v, _ := strconv.ParseInt(s[2:], 16, 0)
		return int(v)
	}
	v, _ := strconv.Atoi(s)
	return v
}

// osInfo returns the OS/platform details for the current runtime.
func osInfo() OSInfo {
	return OSInfo{
		Architecture: runtime.GOARCH,
		Bitness:      strconv.Itoa(strconv.IntSize) + "-bit",
		OSType:       osType(runtime.GOOS),
		Version:      osinfo.OSVersion(),
	}
}

// osType maps a runtime.GOOS value to the Crashtracking os_type label.
func osType(goos string) string {
	switch goos {
	case "linux":
		return "Linux"
	case "darwin":
		return "Mac OS"
	case "windows":
		return "Windows"
	default:
		if goos == "" {
			return ""
		}
		return strings.ToUpper(goos[:1]) + goos[1:]
	}
}
