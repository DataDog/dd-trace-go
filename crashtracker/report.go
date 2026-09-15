// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package crashtracker

import (
	"runtime"
	"strconv"
	"strings"

	"github.com/google/uuid"

	internal "github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/version"
)

// dataSchemaVersion is the libdatadog crashtracker schema version this report
// conforms to. It travels as a ddtags entry, not a top-level field: unlike
// libdatadog's own internal CrashInfo struct, the wire payload libdatadog
// itself sends to errorsintake (ErrorsIntakePayload, in its errors_intake.rs)
// has no data_schema_version/incomplete/uuid fields at the top level — it
// folds them into ddtags instead.
const dataSchemaVersion = "1.8"

// Report is the errorsintake payload sent to Datadog Error Tracking on a crash.
type Report struct {
	Timestamp int64    `json:"timestamp"` // unix ms when the monitor built the report (no crash time is available in the dump)
	DDSource  string   `json:"ddsource"`  // "crashtracker"
	DDTags    string   `json:"ddtags"`    // service,env,version,language_name:go,data_schema_version:...
	Error     Error    `json:"error"`
	OSInfo    OSInfo   `json:"os_info"`
	SigInfo   *SigInfo `json:"sig_info,omitempty"`
	TraceID   string   `json:"trace_id,omitempty"`
}

// Error holds error details in the errorsintake model.
type Error struct {
	Type       string      `json:"type,omitempty"`
	Message    string      `json:"message,omitempty"`
	Stack      *StackTrace `json:"stack,omitempty"`
	Threads    []Thread    `json:"threads,omitempty"`
	ThreadName string      `json:"thread_name,omitempty"`
	IsCrash    bool        `json:"is_crash"`
	SourceType string      `json:"source_type,omitempty"`
}

// StackTrace is the Crashtracking-format structured stack (error.stack object).
type StackTrace struct {
	Format     string  `json:"format"`
	Frames     []Frame `json:"frames"`
	Incomplete bool    `json:"incomplete,omitempty"`
}

// Frame is a single stack frame.
type Frame struct {
	Function string `json:"function,omitempty"`
	File     string `json:"file,omitempty"`
	Line     int    `json:"line,omitempty"`
}

// Thread represents one goroutine in error.threads (flat []Thread per RFC 0011 L331-342).
type Thread struct {
	Crashed bool       `json:"crashed"`
	Name    string     `json:"name"`
	Stack   StackTrace `json:"stack"`
	State   string     `json:"state,omitempty"`
}

// OSInfo holds OS/platform details required by the Crashtracking error.source_type path.
// All four fields are always serialized: libdatadog's own OsInfo struct has no
// optional fields here (os_info.rs), so none of these should be omitempty.
type OSInfo struct {
	Architecture string `json:"architecture"`
	Bitness      string `json:"bitness"`
	OSType       string `json:"os_type"`
	Version      string `json:"version"`
}

// SigInfo holds UNIX signal details for signal-triggered crashes.
type SigInfo struct {
	SiAddr string `json:"si_addr,omitempty"`
	SiCode int    `json:"si_code,omitempty"`
	// SiCodeHuman is declared to match libdatadog's schema, but parseSignal
	// does not currently populate it: the code-to-name mapping (SEGV_MAPERR,
	// BUS_ADRALN, FPE_INTDIV, ...) is both signal- and platform-specific,
	// needing the same kind of per-GOOS table as signalNumbers. Always empty
	// today; see TestParseSignalDoesNotPopulateSiCodeHuman.
	SiCodeHuman  string `json:"si_code_human_readable,omitempty"`
	SiSigno      int    `json:"si_signo,omitempty"`
	SiSignoHuman string `json:"si_signo_human_readable,omitempty"`
}

// buildDDTags constructs the comma-separated ddtags string for a crash
// report, in the same order and with the same key names libdatadog's own
// errors_intake.rs builds it (build_crash_info_tags/append_runtime_tags/
// append_signal_tags): base tags, runtime tags, then the crash-identifying
// tags libdatadog folds into ddtags rather than sending as top-level fields.
func buildDDTags(cfg *config, r *Report) string {
	var b strings.Builder

	writeTag := func(key, value string) {
		if value == "" {
			return
		}
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(key)
		b.WriteByte(':')
		b.WriteString(value)
	}
	writeBool := func(key string, value bool) {
		writeTag(key, strconv.FormatBool(value))
	}
	writeInt := func(key string, value int) {
		// Skip zero to match the JSON model, where si_code/si_signo are
		// omitempty: zero here always means "not parsed from the dump" (both
		// fields are only ever set from a parsed value), never a real signal
		// or code value, so the two views must not disagree over whether the
		// tag is present.
		if value == 0 {
			return
		}
		writeTag(key, strconv.Itoa(value))
	}

	if cfg != nil {
		writeTag("service", cfg.service)
		writeTag("env", cfg.env)
		writeTag("version", cfg.version)
	}
	writeTag("language_name", "go")
	writeTag("language_version", runtime.Version())
	writeTag("tracer_version", version.Tag)
	for k, v := range internal.GetGitMetadataTags() {
		writeTag(k, v)
	}

	writeTag("data_schema_version", dataSchemaVersion)
	writeBool("incomplete", reportIncomplete(r))
	writeBool("is_crash", r.Error.IsCrash)
	writeTag("uuid", newUUID())

	if sig := r.SigInfo; sig != nil {
		writeTag("si_addr", sig.SiAddr)
		writeInt("si_code", sig.SiCode)
		writeTag("si_code_human_readable", sig.SiCodeHuman)
		writeInt("si_signo", sig.SiSigno)
		writeTag("si_signo_human_readable", sig.SiSignoHuman)
	}
	writeTag("runtime_platform", runtime.GOOS+"/"+runtime.GOARCH)

	return b.String()
}

// reportIncomplete reports whether the crashed goroutine's own stack — the
// one Error Tracking groups and displays on — was truncated or missing
// entirely, mirroring libdatadog's CrashInfo.incomplete.
func reportIncomplete(r *Report) bool {
	return r.Error.Stack == nil || r.Error.Stack.Incomplete
}

// newUUID returns a random UUID for the report's ddtags uuid entry.
func newUUID() string {
	return uuid.NewString()
}
