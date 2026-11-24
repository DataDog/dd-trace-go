// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package coverage

import "github.com/tinylib/msgp/msgp"

// coverageRecord is one entry of the coverages of a payload.
type coverageRecord interface {
	msgp.Encodable
	Msgsize() int
}

var (
	_ coverageRecord = (*ciTestCoverageData)(nil)
	_ coverageRecord = (*ciTestCoverageSharedData)(nil)
)

// ciTestCoverageSharedData is one part of Shared: lines the process ran outside
// the tests' own coverage. It has no span ID; records of the same part are
// merged.
type ciTestCoverageSharedData struct {
	SessionID uint64
	Part      string // "init", "suiteSetup" or "unowned"
	Files     []*ciTestCoverageFile
}

// EncodeMsg implements msgp.Encodable.
func (z *ciTestCoverageSharedData) EncodeMsg(en *msgp.Writer) error {
	if err := en.WriteMapHeader(3); err != nil {
		return err
	}
	if err := en.WriteString("test_session_id"); err != nil {
		return err
	}
	if err := en.WriteUint64(z.SessionID); err != nil {
		return err
	}
	if err := en.WriteString("shared"); err != nil {
		return err
	}
	if err := en.WriteString(z.Part); err != nil {
		return err
	}
	if err := en.WriteString("files"); err != nil {
		return err
	}
	if err := en.WriteArrayHeader(uint32(len(z.Files))); err != nil {
		return err
	}
	for _, file := range z.Files {
		if err := file.EncodeMsg(en); err != nil {
			return err
		}
	}
	return nil
}

// Msgsize returns an upper bound of the encoded size.
func (z *ciTestCoverageSharedData) Msgsize() int {
	s := msgp.MapHeaderSize +
		msgp.StringPrefixSize + len("test_session_id") + msgp.Uint64Size +
		msgp.StringPrefixSize + len("shared") + msgp.StringPrefixSize + len(z.Part) +
		msgp.StringPrefixSize + len("files") + msgp.ArrayHeaderSize
	for _, file := range z.Files {
		s += file.Msgsize()
	}
	return s
}
