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
)

// signalNumbers maps the signal names the Go runtime reports on a crash to
// their POSIX signal numbers. Only the signals that surface as fatal crashes
// are included; unknown signals fall back to 0.
var signalNumbers = map[string]int{
	"SIGILL":  4,
	"SIGTRAP": 5,
	"SIGABRT": 6,
	"SIGBUS":  7,
	"SIGFPE":  8,
	"SIGSEGV": 11,
}

// parseCrashDump parses a raw Go crash dump into a Report.
// The input is the full text written by the Go runtime to the crash output fd.
func parseCrashDump(dump []byte) *Report {
	preamble, threads := splitDump(dump)

	sigInfo := parseSignal(preamble)
	errType := errorType(preamble, threads, sigInfo)
	message := errorMessage(preamble, sigInfo)

	crashed := crashingThread(threads)
	var stack *StackTrace
	var threadName string
	if crashed != nil {
		s := crashed.Stack
		stack = &s
		threadName = crashed.Name
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
// stack blocks. The split point is the first goroutine header line.
func splitDump(dump []byte) (preamble []string, threads []Thread) {
	sc := bufio.NewScanner(bytes.NewReader(dump))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var lines []string
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}

	// Find the first goroutine header; everything before it is the preamble.
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

// parseSignal extracts UNIX signal details from the preamble, or returns nil
// if the crash was not signal-triggered.
func parseSignal(preamble []string) *SigInfo {
	for _, line := range preamble {
		if !strings.Contains(line, "signal SIG") {
			continue
		}
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
	return nil
}

// errorType classifies the crash into an error.type value following the
// Crashtracking model.
func errorType(preamble []string, threads []Thread, sigInfo *SigInfo) string {
	// A signal-triggered crash is reported as a UNIX signal regardless of the
	// surrounding panic wrapper the runtime prints.
	if sigInfo != nil {
		return "UnixSignal"
	}
	for _, line := range preamble {
		if rest, ok := strings.CutPrefix(line, "fatal error:"); ok {
			return fatalErrorType(strings.TrimSpace(rest))
		}
		if strings.HasPrefix(line, "panic:") || strings.HasPrefix(line, "panic(") {
			return "panic"
		}
	}
	// A "panic(" frame in the crashing goroutine also indicates a panic.
	if crashed := firstThread(threads); crashed != nil {
		for _, f := range crashed.Stack.Frames {
			if f.Function == "panic" {
				return "panic"
			}
		}
	}
	return "panic"
}

// fatalErrorType maps a "fatal error: <msg>" message to its Go runtime error
// kind. Most fatal errors raised via throw surface as runtime.plainError.
func fatalErrorType(msg string) string {
	switch msg {
	case "":
		return "runtime.Error"
	default:
		return "runtime.plainError"
	}
}

// errorMessage derives the human-readable error message from the preamble.
func errorMessage(preamble []string, sigInfo *SigInfo) string {
	// For signal crashes, prefer the "signal SIG..." line.
	if sigInfo != nil {
		for _, line := range preamble {
			if strings.Contains(line, "signal SIG") {
				return strings.Trim(strings.TrimSpace(line), "[]")
			}
		}
	}
	for _, line := range preamble {
		if rest, ok := strings.CutPrefix(line, "fatal error:"); ok {
			return strings.TrimSpace(rest)
		}
		if rest, ok := strings.CutPrefix(line, "panic:"); ok {
			return strings.TrimSpace(rest)
		}
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

// firstThread returns the crashing goroutine without mutating Crashed flags:
// the first goroutine in the "running" state, or the first overall.
func firstThread(threads []Thread) *Thread {
	if len(threads) == 0 {
		return nil
	}
	for i := range threads {
		if threads[i].State == "running" {
			return &threads[i]
		}
	}
	return &threads[0]
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
		Bitness:      "64-bit",
		OSType:       osType(runtime.GOOS),
		// Version requires an OS-specific syscall; deferred to a follow-up.
		Version: "",
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
