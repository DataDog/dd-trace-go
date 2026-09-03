// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package crashtracker

import (
	"bufio"
	"bytes"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/osinfo"
)

// stackFormat is the stack format identifier reported to Error Tracking.
const stackFormat = "Datadog Crashtracker 1.0"

// ddSource is the ddsource value for crash reports.
const ddSource = "crashtracker"

var (
	// goroutineHeaderRe matches a goroutine block header in both standard and
	// GOTRACEBACK=system format:
	//   standard: "goroutine 1 [running]:"
	//   system:   "goroutine 1 gp=0xc000... m=0 [running]:"
	// The [^[]* skips any extra runtime fields between the id and the state bracket.
	goroutineHeaderRe = regexp.MustCompile(`^goroutine (\d+)[^[]*\[([^\]]+)\]:$`)

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

	// framesElidedRe matches the Go runtime's frame-elision marker for a very
	// deep stack, e.g. "...16777082 frames elided..." — the runtime prints
	// the innermost and outermost 50 logical frames of a goroutine's stack
	// and elides the middle (runtime/traceback.go). It is not a frame itself:
	// it has no source location line following it.
	framesElidedRe = regexp.MustCompile(`^\.\.\.\d+ frames elided\.\.\.$`)
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

	// crashingThread must run before the cap below: it marks the crashed
	// goroutine and we need that goroutine to survive truncation.
	crashed := crashingThread(threads)
	var stack *StackTrace
	var threadName string
	if crashed != nil {
		// error.stack intentionally repeats the crashed goroutine's frames
		// already present in error.threads: the errorsintake schema requires
		// error.stack as a distinct top-level field (see RFC 0011), so this is
		// not redundant to trim away. Note this is a shallow copy — both views
		// share one Frames backing array, so any future in-place rewriting of
		// frames (scrubbing, path trimming) would affect both.
		s := crashed.Stack
		stack = &s
		threadName = crashed.Name
	}
	threads = capThreads(threads)

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
// stack blocks. The split point is the first goroutine header line.
func splitDump(dump []byte) (preamble []string, threads []Thread) {
	sc := bufio.NewScanner(bytes.NewReader(dump))
	// Raise the per-token limit to the dump cap so an oversized panic message
	// does not prevent the scanner from reaching the goroutine stacks below it.
	sc.Buffer(make([]byte, 0, 64*1024), maxCrashDumpSize)

	// Preallocate: rough estimate of 80 bytes per line for typical Go stack frames.
	lines := make([]string, 0, len(dump)/80)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	if err := sc.Err(); err != nil {
		// Scanner errors mean the dump was truncated mid-stream. Mark the output
		// incomplete so callers know the parse is partial rather than silently
		// returning what looks like a complete result.
		preamble = lines
		threads = []Thread{{
			Crashed: true,
			Name:    "goroutine unknown",
			State:   "unknown",
			Stack:   StackTrace{Format: stackFormat, Incomplete: true},
		}}
		return preamble, threads
	}

	// Find the first goroutine header; everything before it is the preamble.
	// Note this also discards the frames of a leading "runtime stack:" block
	// (emitted for stack overflow and for faults during runtime execution),
	// since those precede any goroutine header. That is deliberate: the user
	// goroutine below is the actionable stack for grouping and display.
	start := len(lines)
	for i, line := range lines {
		if goroutineHeaderRe.MatchString(line) {
			start = i
			break
		}
	}

	preamble = lines[:start]
	threads = parseThreads(lines[start:])
	return preamble, threads
}

// parseThreads parses goroutine blocks from the stack portion of the dump.
func parseThreads(lines []string) []Thread {
	var threads []Thread
	i := 0
	for i < len(lines) {
		m := goroutineHeaderRe.FindStringSubmatch(lines[i])
		if m == nil {
			i++
			continue
		}

		thread := Thread{
			Name:  "goroutine " + m[1],
			State: m[2],
			Stack: StackTrace{Format: stackFormat},
		}
		i++

		frames, incomplete, consumed := parseFrames(lines[i:])
		thread.Stack.Frames = frames
		thread.Stack.Incomplete = incomplete
		i += consumed

		threads = append(threads, thread)
	}
	return threads
}

// parseFrames parses the function/location line pairs of a single goroutine
// block. It stops at the next goroutine header or the end of the stack input,
// returning the frames, whether the stack was incomplete, and how many lines
// it consumed.
func parseFrames(lines []string) (frames []Frame, incomplete bool, consumed int) {
	for consumed < len(lines) {
		line := lines[consumed]

		// A blank line or the "exit status" trailer ends this block.
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "exit status") {
			consumed++
			break
		}
		// The next goroutine header ends this block; do not consume it.
		if goroutineHeaderRe.MatchString(line) {
			break
		}

		// "created by <fn> in goroutine N" is a two-line pseudo-frame that
		// records where the goroutine was spawned. It is not part of the
		// goroutine's own stack, so skip both lines.
		if strings.HasPrefix(line, "created by ") {
			consumed++
			if consumed < len(lines) && isLocationLine(lines[consumed]) {
				consumed++
			}
			continue
		}

		// The elision marker has no location line of its own, so treating it
		// as a function line would see the following line as malformed and
		// break out of the whole stack, discarding every frame the runtime
		// resumed printing after it — commonly main and the original call
		// site. Note the gap and continue instead.
		if framesElidedRe.MatchString(line) {
			incomplete = true
			consumed++
			continue
		}

		// Once the cap is hit, skip the function/location line pair without
		// extracting or allocating a Frame — only advancing consumed is still
		// needed so the caller finds the next goroutine header correctly.
		if len(frames) >= maxFramesPerThread {
			incomplete = true
			consumed++
			if consumed < len(lines) && isLocationLine(lines[consumed]) {
				consumed++
			}
			continue
		}

		// Otherwise this is a function line; the following line is its
		// source location.
		fn := funcName(line)
		consumed++
		if consumed >= len(lines) || !isLocationLine(lines[consumed]) {
			// Missing or malformed location: the stack is truncated.
			incomplete = true
			break
		}
		file, lineNo := parseLocation(lines[consumed])
		consumed++

		frames = append(frames, Frame{Function: fn, File: file, Line: lineNo})
	}

	if len(frames) == 0 {
		incomplete = true
	}
	return frames, incomplete, consumed
}

// isLocationLine reports whether a line is an indented source location line,
// e.g. "\t/path/to/file.go:20 +0x58".
func isLocationLine(line string) bool {
	return len(line) > 0 && (line[0] == '\t' || line[0] == ' ')
}

// funcName extracts the function name from a stack function line by stripping
// the argument list. Uses the LAST '(' to correctly handle pointer-receiver
// methods, e.g. "main.(*Server).Serve(0x...)" -> "main.(*Server).Serve".
func funcName(line string) string {
	line = strings.TrimSpace(line)
	if i := strings.LastIndexByte(line, '('); i > 0 {
		return line[:i]
	}
	return line
}

// parseLocation extracts the file path and line number from a location line,
// e.g. "\t/tmp/main.go:20 +0x58" -> ("/tmp/main.go", 20).
func parseLocation(line string) (file string, lineNo int) {
	s := strings.TrimSpace(line)
	// Strip the trailing program-counter offset, e.g. " +0x58".
	if i := strings.Index(s, " +0x"); i >= 0 {
		s = s[:i]
	}
	// The line number follows the last colon.
	if i := strings.LastIndexByte(s, ':'); i >= 0 {
		file = s[:i]
		lineNo, _ = strconv.Atoi(s[i+1:])
		return file, lineNo
	}
	return s, 0
}

// crashingThread returns the goroutine that crashed: the first goroutine in
// the "running" state, or the first goroutine overall if none is running.
func crashingThread(threads []Thread) *Thread {
	if len(threads) == 0 {
		return nil
	}
	idx := 0
	for i := range threads {
		if threads[i].State == "running" {
			idx = i
			break
		}
	}
	threads[idx].Crashed = true
	return &threads[idx]
}

// capThreads bounds the number of goroutines included in a report to
// maxReportThreads, always keeping the crashed goroutine. On the truncating
// path the crashed goroutine is hoisted to the front and the remainder keep
// dump order; under the cap the input order is preserved as-is. The
// errorsintake schema has no field for "N goroutines omitted"; truncation is
// instead observable via the monitor's diagnostic log (see runMonitor).
func capThreads(threads []Thread) []Thread {
	if len(threads) <= maxReportThreads {
		return threads
	}
	kept := make([]Thread, 0, maxReportThreads)
	for _, t := range threads {
		if t.Crashed {
			kept = append(kept, t)
		}
	}
	for _, t := range threads {
		if len(kept) >= maxReportThreads {
			break
		}
		if !t.Crashed {
			kept = append(kept, t)
		}
	}
	log.Warn("crashtracker: report truncated from %d to %d goroutines", len(threads), len(kept))
	return kept
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
