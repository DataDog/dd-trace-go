# Unless explicitly stated otherwise all files in this repository are licensed
# under the Apache License Version 2.0.
# This product includes software developed at Datadog (https://www.datadoghq.com/).
# Copyright 2016 Datadog, Inc.

"""Temporary PR #5474 instrumentation; creates an overlay, never edits sources."""

import hashlib
import json
import subprocess
import sys
from pathlib import Path

HELPER = r'''package http

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime/debug"
	"sync"
)

var goleakDiagnosticPath = os.Getenv("GOLEAK_DIAGNOSTIC_FILE")
var goleakDiagnosticState = struct {
	sync.Mutex
	bytes int64
	live map[string]string
}{live: make(map[string]string)}

func goleakDiagnostic(event string, transport *Transport, connection *persistConn, request *Request) {
	if goleakDiagnosticPath == "" {
		return
	}
	fields := map[string]string{"event": event, "transport": fmt.Sprintf("%p", transport)}
	if connection != nil {
		fields["connection"] = fmt.Sprintf("%p", connection)
		if connection.conn != nil {
			fields["local"] = connection.conn.LocalAddr().String()
			fields["remote"] = connection.conn.RemoteAddr().String()
		}
	}
	if request != nil && request.URL != nil {
		// Do not record headers, request bodies, user info or URL query strings.
		fields["host"] = request.URL.Host
	}
	if event == "request" || event == "open" || event == "use" {
		fields["stack"] = string(debug.Stack())
	}
	goleakDiagnosticState.Lock()
	defer goleakDiagnosticState.Unlock()
	if event == "open" {
		goleakDiagnosticState.live[fields["connection"]] = fields["transport"]
	} else if event == "close" {
		delete(goleakDiagnosticState.live, fields["connection"])
	}
	goleakDiagnosticWriteLocked(fields)
}

func goleakDiagnosticWriteLocked(fields map[string]string) {
	const limit = 50 * 1024 * 1024
	if goleakDiagnosticState.bytes >= limit {
		return
	}
	data, err := json.Marshal(fields)
	if err != nil {
		return
	}
	data = append(data, '\n')
	if goleakDiagnosticState.bytes+int64(len(data)) > limit {
		data = []byte("{\"event\":\"trace-limit\"}\n")
		goleakDiagnosticState.bytes = limit
	}
	file, err := os.OpenFile(goleakDiagnosticPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return
	}
	n, _ := file.Write(data)
	file.Close()
	goleakDiagnosticState.bytes += int64(n)
}

// GoleakDiagnosticSnapshot records open connections immediately before goleak.
func GoleakDiagnosticSnapshot() {
	if goleakDiagnosticPath == "" {
		return
	}
	goleakDiagnosticState.Lock()
	defer goleakDiagnosticState.Unlock()
	goleakDiagnosticWriteLocked(map[string]string{"event": "snapshot", "live": fmt.Sprint(len(goleakDiagnosticState.live))})
	for connection, transport := range goleakDiagnosticState.live {
		goleakDiagnosticWriteLocked(map[string]string{
			"event": "live-after-tests", "connection": connection, "transport": transport,
		})
	}
}
'''


def replace_once(source, before, after):
    if source.count(before) != 1:
        raise RuntimeError(f"Expected one instrumentation anchor: {before!r}")
    return source.replace(before, after, 1)


def main():
    destination = Path(sys.argv[1]).resolve()
    destination.mkdir(parents=True, exist_ok=True)
    root = Path.cwd()
    goroot = Path(subprocess.check_output(["go", "env", "GOROOT"], text=True).strip())
    transport_path = goroot / "src/net/http/transport.go"
    transport = transport_path.read_text()
    transport = replace_once(
        transport,
        "func (t *Transport) roundTrip(req *Request) (_ *Response, err error) {",
        "func (t *Transport) roundTrip(req *Request) (_ *Response, err error) {\n"
        '\tgoleakDiagnostic("request", t, nil, req)',
    )
    transport = replace_once(
        transport,
        "\tgo pconn.readLoop()\n\tgo pconn.writeLoop()",
        '\tgoleakDiagnostic("open", t, pconn, nil)\n'
        "\tgo pconn.readLoop()\n\tgo pconn.writeLoop()",
    )
    transport = replace_once(
        transport,
        "\tif pc.closed == nil {\n\t\tpc.closed = err",
        '\tif pc.closed == nil {\n\t\tgoleakDiagnostic("close", pc.t, pc, nil)\n'
        "\t\tpc.closed = err",
    )
    transport = replace_once(
        transport,
        "func (pc *persistConn) roundTrip(req *transportRequest) (resp *Response, err error) {",
        "func (pc *persistConn) roundTrip(req *transportRequest) (resp *Response, err error) {\n"
        '\tgoleakDiagnostic("use", pc.t, pc, req.Request)',
    )
    test_path = root / "ddtrace/tracer/tracer_test.go"
    test = replace_once(
        test_path.read_text(),
        "\tif code := m.Run(); code != 0 {\n\t\tos.Exit(code)\n\t}",
        "\tcode := m.Run()\n\thttp.GoleakDiagnosticSnapshot()\n"
        "\tif code != 0 {\n\t\tos.Exit(code)\n\t}",
    )
    replacements = {
        transport_path: ("transport.go", transport),
        goroot / "src/net/http/goleak_diagnostic.go": ("goleak_diagnostic.go", HELPER),
        test_path: ("tracer_test.go", test),
    }
    overlay = {}
    for original, (name, content) in replacements.items():
        generated = destination / name
        generated.write_text(content)
        subprocess.run(["gofmt", "-w", str(generated)], check=True)
        overlay[str(original)] = str(generated)
    (destination / "overlay.json").write_text(json.dumps({"Replace": overlay}) + "\n")
    inputs = {
        str(path): hashlib.sha256(path.read_bytes()).hexdigest()
        for path in (transport_path, test_path, root / "go.mod", root / "go.sum")
    }
    (destination / "input-hashes.json").write_text(json.dumps(inputs, indent=2) + "\n")
    print(f"Generated temporary overlay in {destination}")


if __name__ == "__main__":
    main()
