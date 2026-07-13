// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package crashtracker

// Report is the errorsintake payload sent to Datadog Error Tracking on a crash.
type Report struct {
	Timestamp int64    `json:"timestamp"`          // unix ms at crash time
	DDSource  string   `json:"ddsource"`           // "crashtracker"
	DDTags    string   `json:"ddtags"`             // service,env,version,language:go,...
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
type OSInfo struct {
	Architecture string `json:"architecture"`
	Bitness      string `json:"bitness,omitempty"`
	OSType       string `json:"os_type,omitempty"`
	Version      string `json:"version,omitempty"`
}

// SigInfo holds UNIX signal details for signal-triggered crashes.
type SigInfo struct {
	SiAddr       string `json:"si_addr,omitempty"`
	SiCode       int    `json:"si_code,omitempty"`
	SiCodeHuman  string `json:"si_code_human_readable,omitempty"`
	SiSigno      int    `json:"si_signo,omitempty"`
	SiSignoHuman string `json:"si_signo_human_readable,omitempty"`
}
