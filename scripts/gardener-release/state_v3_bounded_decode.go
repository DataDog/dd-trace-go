// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog, Inc.
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"crypto/sha256"
	"errors"
	"unicode/utf8"
)

const (
	stateV3BoundedCompactDocumentBytes = 16 * 1024
	stateV3BoundedJSONMaxMembers       = 24
)

var errstateV3BoundedDocument = errors.New("invalid bounded state v3 document")

// StateV3BoundedWorkspace is fixed parser scratch for one bounded document.
// Returned models are self-contained fixed values and never alias this
// workspace or the caller's input. Call Reset before reusing the workspace.
type StateV3BoundedWorkspace struct {
	cursor uint16
}

// Reset clears all fixed parser state.
func (w *StateV3BoundedWorkspace) Reset() {
	*w = StateV3BoundedWorkspace{}
}

// stateV3BoundedDocument is private parser state; callers receive one of the
// purpose-specific fixed models below.
type stateV3BoundedDocument struct {
	kind     uint8
	size     uint16
	digest   [sha256.Size]byte
	arena    [stateV3BoundedCompactDocumentBytes]byte
	used     uint16
	fields   [stateV3BoundedJSONMaxMembers]stateV3BoundedField
	response [3]stateV3BoundedField
}

// stateV3BoundedField holds one decoded compact-document value without a Go
// string, slice, map, pointer, or interface. Fields are in canonical schema
// order; absent optional fields have present set to false.
type stateV3BoundedField struct {
	start   uint16
	length  uint16
	number  int64
	present bool
}

const (
	stateV3BoundedLease uint8 = iota + 1
	stateV3BoundedClaim
	stateV3BoundedArm
	stateV3BoundedOutcome
)

type stateV3BoundedKey struct {
	name     string
	required bool
}

// DecodeStateV3ActiveLeaseBounded structurally decodes a byte-exact canonical
// active-lease document into a fixed model using only caller-provided scratch.
// It establishes neither lifecycle authority nor document provenance.
func DecodeStateV3ActiveLeaseBounded(raw []byte, workspace *StateV3BoundedWorkspace) (StateV3BoundedActiveLease, error) {
	document, err := decodeStateV3BoundedCompact(raw, workspace, stateV3BoundedLease)
	if err != nil {
		return StateV3BoundedActiveLease{}, err
	}
	developmentVersion := document.fields[7]
	if developmentVersion.present && developmentVersion.length == 0 {
		return StateV3BoundedActiveLease{}, errstateV3BoundedDocument
	}
	return StateV3BoundedActiveLease{document: document}, nil
}

// DecodeStateV3CoordinationClaimBounded structurally decodes a byte-exact
// canonical coordination claim into a fixed model. It establishes neither
// lifecycle authority nor document provenance.
func DecodeStateV3CoordinationClaimBounded(raw []byte, workspace *StateV3BoundedWorkspace) (StateV3BoundedCoordinationClaim, error) {
	document, err := decodeStateV3BoundedCompact(raw, workspace, stateV3BoundedClaim)
	if err != nil {
		return StateV3BoundedCoordinationClaim{}, err
	}
	developmentVersion := document.fields[2]
	if developmentVersion.present && developmentVersion.length == 0 {
		return StateV3BoundedCoordinationClaim{}, errstateV3BoundedDocument
	}
	return StateV3BoundedCoordinationClaim{document: document}, nil
}

// DecodeStateV3CoordinationArmBounded structurally decodes a byte-exact
// canonical coordination mutation arm into a fixed model. It establishes
// neither lifecycle authority nor document provenance.
func DecodeStateV3CoordinationArmBounded(raw []byte, workspace *StateV3BoundedWorkspace) (StateV3BoundedCoordinationArm, error) {
	document, err := decodeStateV3BoundedCompact(raw, workspace, stateV3BoundedArm)
	if err != nil {
		return StateV3BoundedCoordinationArm{}, err
	}
	return StateV3BoundedCoordinationArm{document: document}, nil
}

// DecodeStateV3CoordinationOutcomeBounded structurally decodes a byte-exact
// canonical mutation outcome into a fixed model. It establishes neither
// lifecycle authority nor document provenance.
func DecodeStateV3CoordinationOutcomeBounded(raw []byte, workspace *StateV3BoundedWorkspace) (StateV3BoundedCoordinationOutcome, error) {
	document, err := decodeStateV3BoundedCompact(raw, workspace, stateV3BoundedOutcome)
	if err != nil {
		return StateV3BoundedCoordinationOutcome{}, err
	}
	return StateV3BoundedCoordinationOutcome{document: document}, nil
}

var stateV3LeaseSchema = [20]stateV3BoundedKey{
	{"command", true}, {"coordination_claim_blob_oid", true}, {"coordination_claim_oid", true},
	{"coordination_claim_path", true}, {"coordination_claim_sha256", true}, {"coordination_parent_oid", true},
	{"coordination_ref", true}, {"development_version", false}, {"original_comment_id", true},
	{"repository_full_name", true}, {"repository_id", true}, {"request_key", true}, {"request_sha256", true},
	{"requested_version", true}, {"reservation_marker", true}, {"resolved_version", true},
	{"schema_version", true}, {"source_oid", true}, {"source_ref", true}, {"version_resolution_sha256", true},
}

var stateV3ClaimSchema = [13]stateV3BoundedKey{
	{"attempt", true}, {"command", true}, {"development_version", false}, {"lane_expected_head_oid", true},
	{"lane_ref", true}, {"phase", true}, {"release_line", true}, {"request_key", true},
	{"request_sha256", true}, {"reservation_sha256", true}, {"resolved_version", true}, {"state", true}, {"version_resolution_sha256", true},
}

var stateV3ArmSchema = [11]stateV3BoundedKey{
	{"attempt", true}, {"claim_path", true}, {"expected_claim_blob_oid", false}, {"expected_head_oid", true},
	{"intended_claim_sha256", false}, {"lane_ref", true}, {"operation", true}, {"ref", true},
	{"release_line", true}, {"request_key", true}, {"resolved_version", true},
}

var stateV3OutcomeSchema = [23]stateV3BoundedKey{
	{"arm_blob_oid", true}, {"arm_commit_oid", true}, {"arm_path", true}, {"arm_sha256", true},
	{"arm_tree_oid", true}, {"claim_blob_oid", false}, {"claim_commit_oid", false}, {"claim_path", true},
	{"claim_sha256", false}, {"claim_tree_oid", false}, {"expected_claim_blob_oid", false},
	{"expected_head_oid", true}, {"intended_claim_sha256", false}, {"lane_ref", true}, {"observed_ref_oid", true},
	{"operation", true}, {"release_line", true}, {"request_key", true}, {"request_sha256", true},
	{"resolved_version", true}, {"response", true}, {"schema_version", true}, {"state_ref", true},
}

func decodeStateV3BoundedCompact(raw []byte, workspace *StateV3BoundedWorkspace, kind uint8) (stateV3BoundedDocument, error) {
	if workspace == nil || len(raw) == 0 || len(raw) > stateV3BoundedCompactDocumentBytes {
		return stateV3BoundedDocument{}, errstateV3BoundedDocument
	}
	workspace.Reset()
	document := stateV3BoundedDocument{kind: kind, size: uint16(len(raw)), digest: sha256.Sum256(raw)}
	p := stateV3BoundedParser{raw: raw, workspace: workspace, document: &document}
	var ok bool
	switch kind {
	case stateV3BoundedLease:
		ok = p.leaseObject()
	case stateV3BoundedClaim:
		ok = p.claimObject()
	case stateV3BoundedArm:
		ok = p.armObject()
	case stateV3BoundedOutcome:
		ok = p.outcomeObject()
	}
	if !ok || p.at != len(raw) {
		workspace.Reset()
		return stateV3BoundedDocument{}, errstateV3BoundedDocument
	}
	return document, nil
}

type stateV3BoundedParser struct {
	raw       []byte
	at        int
	workspace *StateV3BoundedWorkspace
	document  *stateV3BoundedDocument
}

func (p *stateV3BoundedParser) leaseObject() bool {
	return p.object(stateV3LeaseSchema[:], stateV3BoundedKey{})
}

func (p *stateV3BoundedParser) claimObject() bool {
	return p.object(stateV3ClaimSchema[:], stateV3BoundedKey{})
}

func (p *stateV3BoundedParser) armObject() bool {
	return p.object(stateV3ArmSchema[:], stateV3BoundedKey{})
}

func (p *stateV3BoundedParser) outcomeObject() bool {
	return p.object(stateV3OutcomeSchema[:], stateV3BoundedKey{})
}

func (p *stateV3BoundedParser) object(schema []stateV3BoundedKey, tail stateV3BoundedKey) bool {
	if !p.consume('{') {
		return false
	}
	member := false
	for index := 0; index < len(schema)+1; index++ {
		key := tail
		if index < len(schema) {
			key = schema[index]
		}
		if key.name == "" {
			continue
		}
		beforeComma := p.at
		if member && p.at < len(p.raw) && p.raw[p.at] == ',' {
			p.at++
		}
		if !p.matchesKey(key.name) {
			if key.required {
				return false
			}
			p.at = beforeComma
			continue
		}
		if !p.key(key.name) || !p.consume(':') || !p.typedValue(key.name, index) {
			return false
		}
		member = true
	}
	return member && p.consume('}')
}

func (p *stateV3BoundedParser) matchesKey(expected string) bool {
	if p.at >= len(p.raw) || p.raw[p.at] != '"' || len(p.raw)-p.at < len(expected)+2 {
		return false
	}
	for index := 0; index < len(expected); index++ {
		if p.raw[p.at+1+index] != expected[index] {
			return false
		}
	}
	return p.raw[p.at+len(expected)+1] == '"'
}

func (p *stateV3BoundedParser) key(expected string) bool {
	start := p.at
	if !p.stringToken() {
		return false
	}
	if p.at-start != len(expected)+2 {
		return false
	}
	for index := 0; index < len(expected); index++ {
		if p.raw[start+1+index] != expected[index] {
			return false
		}
	}
	return true
}

func (p *stateV3BoundedParser) typedValue(key string, index int) bool {
	if key == "attempt" {
		return p.numberInto(index)
	}
	if key == "response" {
		return p.responseObject(index)
	}
	return p.stringInto(index)
}

func (p *stateV3BoundedParser) responseObject(_ int) bool {
	if !p.consume('{') || !p.key("attempts") || !p.consume(':') || !p.numberIntoResponse(0) || !p.consume(',') || !p.key("observation") || !p.consume(':') || !p.stringIntoResponse(1) || !p.consume(',') || !p.key("oid") || !p.consume(':') || !p.stringIntoResponse(2) || !p.consume('}') {
		return false
	}
	return true
}

func (p *stateV3BoundedParser) stringToken() bool {
	return p.stringInto(-1)
}

func (p *stateV3BoundedParser) stringIntoResponse(index int) bool {
	if index < 0 || index >= len(p.document.response) {
		return false
	}
	return p.stringIntoField(&p.document.response[index])
}

func (p *stateV3BoundedParser) numberIntoResponse(index int) bool {
	start := p.at
	if !p.number() || index < 0 || index >= len(p.document.response) {
		return false
	}
	value, ok := stateV3BoundedInt64(p.raw[start:p.at])
	if !ok {
		return false
	}
	p.document.response[index].number, p.document.response[index].present = value, true
	return true
}

func (p *stateV3BoundedParser) stringInto(field int) bool {
	if field < 0 {
		return p.stringIntoField(nil)
	}
	if field >= len(p.document.fields) {
		return false
	}
	return p.stringIntoField(&p.document.fields[field])
}

func (p *stateV3BoundedParser) stringIntoField(value *stateV3BoundedField) bool {
	start := p.at
	if !p.consume('"') {
		return false
	}
	if value != nil {
		*value = stateV3BoundedField{start: p.document.used, present: true}
	}
	appendByte := func(b byte) bool {
		if value == nil {
			return true
		}
		if int(p.document.used) >= len(p.document.arena) {
			return false
		}
		p.document.arena[p.document.used] = b
		p.document.used++
		value.length++
		return true
	}
	appendRune := func(r rune) bool {
		var b [utf8.UTFMax]byte
		n := utf8.EncodeRune(b[:], r)
		for i := 0; i < n; i++ {
			if !appendByte(b[i]) {
				return false
			}
		}
		return true
	}
	for p.at < len(p.raw) {
		b := p.raw[p.at]
		switch {
		case b == '"':
			p.at++
			if value == nil {
				return true
			}
			return stateV3CanonicalJSONString(p.raw[start:p.at], p.document.arena[value.start:uint32(value.start)+uint32(value.length)])
		case b < 0x20:
			return false
		case b == '\\':
			p.at++
			if p.at >= len(p.raw) {
				return false
			}
			e := p.raw[p.at]
			p.at++
			switch e {
			case '"', '\\', '/':
				if !appendByte(e) {
					return false
				}
			case 'b':
				if !appendByte('\b') {
					return false
				}
			case 'f':
				if !appendByte('\f') {
					return false
				}
			case 'n':
				if !appendByte('\n') {
					return false
				}
			case 'r':
				if !appendByte('\r') {
					return false
				}
			case 't':
				if !appendByte('\t') {
					return false
				}
			case 'u':
				if p.at+4 > len(p.raw) {
					return false
				}
				var c uint16
				for i := 0; i < 4; i++ {
					x := p.raw[p.at+i]
					c <<= 4
					if x >= '0' && x <= '9' {
						c |= uint16(x - '0')
					} else if x >= 'a' && x <= 'f' {
						c |= uint16(x - 'a' + 10)
					} else if x >= 'A' && x <= 'F' {
						c |= uint16(x - 'A' + 10)
					} else {
						return false
					}
				}
				p.at += 4
				if c >= 0xd800 && c <= 0xdbff {
					if p.at+6 > len(p.raw) || p.raw[p.at] != '\\' || p.raw[p.at+1] != 'u' {
						return false
					}
					var l uint16
					for i := 0; i < 4; i++ {
						x := p.raw[p.at+2+i]
						l <<= 4
						if x >= '0' && x <= '9' {
							l |= uint16(x - '0')
						} else if x >= 'a' && x <= 'f' {
							l |= uint16(x - 'a' + 10)
						} else if x >= 'A' && x <= 'F' {
							l |= uint16(x - 'A' + 10)
						} else {
							return false
						}
					}
					if l < 0xdc00 || l > 0xdfff {
						return false
					}
					p.at += 6
					if !appendRune(rune(0x10000 + ((uint32(c) - 0xd800) << 10) + (uint32(l) - 0xdc00))) {
						return false
					}
				} else if c >= 0xdc00 && c <= 0xdfff {
					return false
				} else if !appendRune(rune(c)) {
					return false
				}
			default:
				return false
			}
		case b < utf8.RuneSelf:
			p.at++
			if !appendByte(b) {
				return false
			}
		default:
			r, w := utf8.DecodeRune(p.raw[p.at:])
			if r == utf8.RuneError && w == 1 {
				return false
			}
			for i := 0; i < w; i++ {
				if !appendByte(p.raw[p.at+i]) {
					return false
				}
			}
			p.at += w
		}
	}
	return false
}

func (p *stateV3BoundedParser) numberInto(field int) bool {
	start := p.at
	if !p.number() || field < 0 || field >= len(p.document.fields) {
		return false
	}
	value, ok := stateV3BoundedInt64(p.raw[start:p.at])
	if !ok {
		return false
	}
	p.document.fields[field].number, p.document.fields[field].present = value, true
	return true
}

// stateV3BoundedInt64 accepts the portable range shared by every supported
// Go int architecture. The dynamic documents use int fields, so values outside
// this range could decode on 64-bit targets but not on 32-bit targets.
func stateV3BoundedInt64(raw []byte) (int64, bool) {
	if len(raw) == 0 {
		return 0, false
	}
	negative := raw[0] == '-'
	if negative {
		raw = raw[1:]
	}
	if len(raw) == 0 || (raw[0] == '0' && len(raw) > 1) {
		return 0, false
	}
	const maximum = int64(1 << 31)
	var value int64
	for _, b := range raw {
		if b < '0' || b > '9' || value > (maximum-int64(b-'0'))/10 {
			return 0, false
		}
		value = value*10 + int64(b-'0')
	}
	if negative {
		return -value, value <= maximum
	}
	return value, value < maximum
}

func stateV3CanonicalJSONString(raw, decoded []byte) bool {
	if len(raw) < 2 || raw[0] != '"' || raw[len(raw)-1] != '"' {
		return false
	}
	at := 1
	consume := func(want byte) bool {
		if at >= len(raw)-1 || raw[at] != want {
			return false
		}
		at++
		return true
	}
	escaped := func(short byte) bool { return consume('\\') && consume(short) }
	hex := "0123456789abcdef"
	unicodeEscape := func(value rune) bool {
		return escaped('u') && consume(hex[(value>>12)&15]) && consume(hex[(value>>8)&15]) && consume(hex[(value>>4)&15]) && consume(hex[value&15])
	}
	for index := 0; index < len(decoded); {
		b := decoded[index]
		if b < utf8.RuneSelf {
			index++
			switch b {
			case '"':
				if !escaped('"') {
					return false
				}
			case '\\':
				if !escaped('\\') {
					return false
				}
			case '\b':
				if !escaped('b') {
					return false
				}
			case '\f':
				if !escaped('f') {
					return false
				}
			case '\n':
				if !escaped('n') {
					return false
				}
			case '\r':
				if !escaped('r') {
					return false
				}
			case '\t':
				if !escaped('t') {
					return false
				}
			case '<', '>', '&':
				if !unicodeEscape(rune(b)) {
					return false
				}
			default:
				if b < 0x20 {
					if !unicodeEscape(rune(b)) {
						return false
					}
				} else if !consume(b) {
					return false
				}
			}
			continue
		}
		r, width := utf8.DecodeRune(decoded[index:])
		if r == utf8.RuneError && width == 1 {
			return false
		}
		if r == '\u2028' || r == '\u2029' {
			if !unicodeEscape(r) {
				return false
			}
		} else {
			for _, encoded := range decoded[index : index+width] {
				if !consume(encoded) {
					return false
				}
			}
		}
		index += width
	}
	return at == len(raw)-1
}

func (p *stateV3BoundedParser) number() bool {
	start := p.at
	if p.at < len(p.raw) && p.raw[p.at] == '-' {
		p.at++
	}
	if p.at >= len(p.raw) || p.raw[p.at] < '0' || p.raw[p.at] > '9' {
		return false
	}
	if p.raw[p.at] == '0' {
		p.at++
		return p.at == len(p.raw) || p.raw[p.at] < '0' || p.raw[p.at] > '9'
	}
	for p.at < len(p.raw) && p.raw[p.at] >= '0' && p.raw[p.at] <= '9' {
		p.at++
	}
	return p.at > start
}

func (p *stateV3BoundedParser) literal(expected string) bool {
	if len(p.raw)-p.at < len(expected) {
		return false
	}
	for index := 0; index < len(expected); index++ {
		if p.raw[p.at+index] != expected[index] {
			return false
		}
	}
	p.at += len(expected)
	return true
}

func (p *stateV3BoundedParser) consume(value byte) bool {
	if p.at >= len(p.raw) || p.raw[p.at] != value {
		return false
	}
	p.at++
	p.workspace.cursor = uint16(p.at)
	return true
}

// StateV3BoundedActiveLease is an opaque fixed-arena structural model.
type StateV3BoundedActiveLease struct{ document stateV3BoundedDocument }
type StateV3BoundedCoordinationClaim struct{ document stateV3BoundedDocument }
type StateV3BoundedCoordinationArm struct{ document stateV3BoundedDocument }
type StateV3BoundedCoordinationOutcome struct{ document stateV3BoundedDocument }

// Reset clears all decoded document bytes and metadata.
func (m *StateV3BoundedActiveLease) Reset()         { *m = StateV3BoundedActiveLease{} }
func (m *StateV3BoundedCoordinationClaim) Reset()   { *m = StateV3BoundedCoordinationClaim{} }
func (m *StateV3BoundedCoordinationArm) Reset()     { *m = StateV3BoundedCoordinationArm{} }
func (m *StateV3BoundedCoordinationOutcome) Reset() { *m = StateV3BoundedCoordinationOutcome{} }

// stateV3BoundedExpectationSpan identifies one value in an expectation's
// private fixed arena.
type stateV3BoundedExpectationSpan struct {
	start  uint16
	length uint16
}

// StateV3BoundedClaimAcquireArmExpectation is an exact fixed expectation for
// an observed claim-acquire arm. All text values share one private 16 KiB
// arena and never alias constructor input.
type StateV3BoundedClaimAcquireArmExpectation struct {
	arena                                                 [stateV3BoundedCompactDocumentBytes]byte
	ref, claimPath, requestKey, releaseLine, laneRef      stateV3BoundedExpectationSpan
	resolvedVersion, expectedHeadOID, intendedClaimSHA256 stateV3BoundedExpectationSpan
	attempt                                               int32
}

// StateV3BoundedClaimReleaseArmExpectation is an exact fixed expectation for
// an observed claim-release arm. All text values share one private 16 KiB
// arena and never alias constructor input.
type StateV3BoundedClaimReleaseArmExpectation struct {
	arena                                                  [stateV3BoundedCompactDocumentBytes]byte
	ref, claimPath, requestKey, releaseLine, laneRef       stateV3BoundedExpectationSpan
	resolvedVersion, expectedHeadOID, expectedClaimBlobOID stateV3BoundedExpectationSpan
	attempt                                                int32
}

// StateV3BoundedActiveClaimExpectation is an exact fixed expectation for an
// active, reserved coordination claim. It establishes document-local
// correlation only; it does not establish claim provenance or lifecycle.
type StateV3BoundedActiveClaimExpectation struct {
	arena                                                                                            [stateV3BoundedCompactDocumentBytes]byte
	command, laneExpectedHeadOID, laneRef, releaseLine, requestKey, requestSHA256, reservationSHA256 stateV3BoundedExpectationSpan
	resolvedVersion, versionResolutionSHA256, developmentVersion                                     stateV3BoundedExpectationSpan
	developmentVersionPresent                                                                        bool
}

// StateV3BoundedClaimAcquireOutcomeExpectation is an exact fixed expectation
// for an observed claim-acquire outcome. All text values share one private
// 16 KiB arena and never alias constructor input.
type StateV3BoundedClaimAcquireOutcomeExpectation struct {
	arena                                                                             [stateV3BoundedCompactDocumentBytes]byte
	schemaVersion, stateRef, armPath, armBlobOID, armSHA256, armCommitOID, armTreeOID stateV3BoundedExpectationSpan
	claimPath, requestKey, requestSHA256, releaseLine, laneRef, resolvedVersion       stateV3BoundedExpectationSpan
	expectedHeadOID, intendedClaimSHA256, responseOID, observedRefOID                 stateV3BoundedExpectationSpan
	responseAttempts                                                                  int32
}

// StateV3BoundedClaimReleaseOutcomeExpectation is an exact fixed expectation
// for an observed claim-release outcome. All text values share one private
// 16 KiB arena and never alias constructor input.
type StateV3BoundedClaimReleaseOutcomeExpectation struct {
	arena                                                                                          [stateV3BoundedCompactDocumentBytes]byte
	schemaVersion, stateRef, armPath, armBlobOID, armSHA256, armCommitOID, armTreeOID              stateV3BoundedExpectationSpan
	claimPath, requestKey, requestSHA256, releaseLine, laneRef, resolvedVersion                    stateV3BoundedExpectationSpan
	expectedHeadOID, claimBlobOID, claimSHA256, claimCommitOID, claimTreeOID, expectedClaimBlobOID stateV3BoundedExpectationSpan
	responseOID, observedRefOID                                                                    stateV3BoundedExpectationSpan
	responseAttempts                                                                               int32
}

// NewStateV3BoundedClaimAcquireArmExpectation copies byte-backed values into
// one fixed arena for exact claim-acquire arm correlation.
func NewStateV3BoundedClaimAcquireArmExpectation(ref, claimPath, requestKey, releaseLine, laneRef, resolvedVersion, expectedHeadOID, intendedClaimSHA256 []byte, attempt int32) (StateV3BoundedClaimAcquireArmExpectation, bool) {
	var expected StateV3BoundedClaimAcquireArmExpectation
	var ok bool
	if expected.ref, ok = stateV3BoundedExpectationAppend(&expected.arena, 0, ref); !ok {
		return StateV3BoundedClaimAcquireArmExpectation{}, false
	}
	used := expected.ref.length
	if expected.claimPath, ok = stateV3BoundedExpectationAppend(&expected.arena, used, claimPath); !ok {
		return StateV3BoundedClaimAcquireArmExpectation{}, false
	}
	used += expected.claimPath.length
	if expected.requestKey, ok = stateV3BoundedExpectationAppend(&expected.arena, used, requestKey); !ok {
		return StateV3BoundedClaimAcquireArmExpectation{}, false
	}
	used += expected.requestKey.length
	if expected.releaseLine, ok = stateV3BoundedExpectationAppend(&expected.arena, used, releaseLine); !ok {
		return StateV3BoundedClaimAcquireArmExpectation{}, false
	}
	used += expected.releaseLine.length
	if expected.laneRef, ok = stateV3BoundedExpectationAppend(&expected.arena, used, laneRef); !ok {
		return StateV3BoundedClaimAcquireArmExpectation{}, false
	}
	used += expected.laneRef.length
	if expected.resolvedVersion, ok = stateV3BoundedExpectationAppend(&expected.arena, used, resolvedVersion); !ok {
		return StateV3BoundedClaimAcquireArmExpectation{}, false
	}
	used += expected.resolvedVersion.length
	if expected.expectedHeadOID, ok = stateV3BoundedExpectationAppend(&expected.arena, used, expectedHeadOID); !ok {
		return StateV3BoundedClaimAcquireArmExpectation{}, false
	}
	used += expected.expectedHeadOID.length
	if expected.intendedClaimSHA256, ok = stateV3BoundedExpectationAppend(&expected.arena, used, intendedClaimSHA256); !ok {
		return StateV3BoundedClaimAcquireArmExpectation{}, false
	}
	expected.attempt = attempt
	return expected, true
}

// NewStateV3BoundedClaimReleaseArmExpectation copies byte-backed values into
// one fixed arena for exact claim-release arm correlation.
func NewStateV3BoundedClaimReleaseArmExpectation(ref, claimPath, requestKey, releaseLine, laneRef, resolvedVersion, expectedHeadOID, expectedClaimBlobOID []byte, attempt int32) (StateV3BoundedClaimReleaseArmExpectation, bool) {
	var expected StateV3BoundedClaimReleaseArmExpectation
	spans := []*stateV3BoundedExpectationSpan{&expected.ref, &expected.claimPath, &expected.requestKey, &expected.releaseLine, &expected.laneRef, &expected.resolvedVersion, &expected.expectedHeadOID, &expected.expectedClaimBlobOID}
	values := [][]byte{ref, claimPath, requestKey, releaseLine, laneRef, resolvedVersion, expectedHeadOID, expectedClaimBlobOID}
	if !stateV3BoundedExpectationAppendAll(&expected.arena, spans, values) {
		return StateV3BoundedClaimReleaseArmExpectation{}, false
	}
	expected.attempt = attempt
	return expected, true
}

// NewStateV3BoundedActiveClaimExpectation copies byte-backed values into one
// fixed arena for exact active, reserved claim correlation.
func NewStateV3BoundedActiveClaimExpectation(command, developmentVersion []byte, developmentVersionPresent bool, laneExpectedHeadOID, laneRef, releaseLine, requestKey, requestSHA256, reservationSHA256, resolvedVersion, versionResolutionSHA256 []byte) (StateV3BoundedActiveClaimExpectation, bool) {
	if (!developmentVersionPresent && len(developmentVersion) != 0) || (developmentVersionPresent && len(developmentVersion) == 0) {
		return StateV3BoundedActiveClaimExpectation{}, false
	}
	var expected StateV3BoundedActiveClaimExpectation
	spans := []*stateV3BoundedExpectationSpan{&expected.command, &expected.laneExpectedHeadOID, &expected.laneRef, &expected.releaseLine, &expected.requestKey, &expected.requestSHA256, &expected.reservationSHA256, &expected.resolvedVersion, &expected.versionResolutionSHA256}
	values := [][]byte{command, laneExpectedHeadOID, laneRef, releaseLine, requestKey, requestSHA256, reservationSHA256, resolvedVersion, versionResolutionSHA256}
	if developmentVersionPresent {
		spans = append(spans, &expected.developmentVersion)
		values = append(values, developmentVersion)
	}
	if !stateV3BoundedExpectationAppendAll(&expected.arena, spans, values) {
		return StateV3BoundedActiveClaimExpectation{}, false
	}
	expected.developmentVersionPresent = developmentVersionPresent
	return expected, true
}

// NewStateV3BoundedClaimAcquireOutcomeExpectation copies byte-backed values
// into one fixed arena for exact claim-acquire outcome correlation.
func NewStateV3BoundedClaimAcquireOutcomeExpectation(schemaVersion, stateRef, armPath, armBlobOID, armSHA256, armCommitOID, armTreeOID, claimPath, requestKey, requestSHA256, releaseLine, laneRef, resolvedVersion, expectedHeadOID, intendedClaimSHA256, responseOID, observedRefOID []byte, responseAttempts int32) (StateV3BoundedClaimAcquireOutcomeExpectation, bool) {
	var expected StateV3BoundedClaimAcquireOutcomeExpectation
	spans := []*stateV3BoundedExpectationSpan{&expected.schemaVersion, &expected.stateRef, &expected.armPath, &expected.armBlobOID, &expected.armSHA256, &expected.armCommitOID, &expected.armTreeOID, &expected.claimPath, &expected.requestKey, &expected.requestSHA256, &expected.releaseLine, &expected.laneRef, &expected.resolvedVersion, &expected.expectedHeadOID, &expected.intendedClaimSHA256, &expected.responseOID, &expected.observedRefOID}
	values := [][]byte{schemaVersion, stateRef, armPath, armBlobOID, armSHA256, armCommitOID, armTreeOID, claimPath, requestKey, requestSHA256, releaseLine, laneRef, resolvedVersion, expectedHeadOID, intendedClaimSHA256, responseOID, observedRefOID}
	if !stateV3BoundedExpectationAppendAll(&expected.arena, spans, values) {
		return StateV3BoundedClaimAcquireOutcomeExpectation{}, false
	}
	expected.responseAttempts = responseAttempts
	return expected, true
}

// NewStateV3BoundedClaimReleaseOutcomeExpectation copies byte-backed values
// into one fixed arena for exact claim-release outcome correlation.
func NewStateV3BoundedClaimReleaseOutcomeExpectation(schemaVersion, stateRef, armPath, armBlobOID, armSHA256, armCommitOID, armTreeOID, claimPath, requestKey, requestSHA256, releaseLine, laneRef, resolvedVersion, expectedHeadOID, claimBlobOID, claimSHA256, claimCommitOID, claimTreeOID, expectedClaimBlobOID, responseOID, observedRefOID []byte, responseAttempts int32) (StateV3BoundedClaimReleaseOutcomeExpectation, bool) {
	var expected StateV3BoundedClaimReleaseOutcomeExpectation
	spans := []*stateV3BoundedExpectationSpan{&expected.schemaVersion, &expected.stateRef, &expected.armPath, &expected.armBlobOID, &expected.armSHA256, &expected.armCommitOID, &expected.armTreeOID, &expected.claimPath, &expected.requestKey, &expected.requestSHA256, &expected.releaseLine, &expected.laneRef, &expected.resolvedVersion, &expected.expectedHeadOID, &expected.claimBlobOID, &expected.claimSHA256, &expected.claimCommitOID, &expected.claimTreeOID, &expected.expectedClaimBlobOID, &expected.responseOID, &expected.observedRefOID}
	values := [][]byte{schemaVersion, stateRef, armPath, armBlobOID, armSHA256, armCommitOID, armTreeOID, claimPath, requestKey, requestSHA256, releaseLine, laneRef, resolvedVersion, expectedHeadOID, claimBlobOID, claimSHA256, claimCommitOID, claimTreeOID, expectedClaimBlobOID, responseOID, observedRefOID}
	if !stateV3BoundedExpectationAppendAll(&expected.arena, spans, values) {
		return StateV3BoundedClaimReleaseOutcomeExpectation{}, false
	}
	expected.responseAttempts = responseAttempts
	return expected, true
}

func stateV3BoundedExpectationAppend(arena *[stateV3BoundedCompactDocumentBytes]byte, used uint16, value []byte) (stateV3BoundedExpectationSpan, bool) {
	start := int(used)
	if start > len(arena) || len(value) > len(arena)-start {
		return stateV3BoundedExpectationSpan{}, false
	}
	copy(arena[start:], value)
	return stateV3BoundedExpectationSpan{start: used, length: uint16(len(value))}, true
}

func stateV3BoundedExpectationAppendAll(arena *[stateV3BoundedCompactDocumentBytes]byte, spans []*stateV3BoundedExpectationSpan, values [][]byte) bool {
	if len(spans) != len(values) {
		return false
	}
	var used uint16
	for i, value := range values {
		span, ok := stateV3BoundedExpectationAppend(arena, used, value)
		if !ok {
			return false
		}
		*spans[i] = span
		used += span.length
	}
	return true
}

func stateV3BoundedExpectationMatches(field stateV3BoundedField, document stateV3BoundedDocument, arena [stateV3BoundedCompactDocumentBytes]byte, expected stateV3BoundedExpectationSpan) bool {
	fieldStart, fieldLength := int(field.start), int(field.length)
	expectedStart, expectedLength := int(expected.start), int(expected.length)
	if !field.present || fieldLength != expectedLength || fieldStart > len(document.arena) || fieldLength > len(document.arena)-fieldStart || expectedStart > len(arena) || expectedLength > len(arena)-expectedStart {
		return false
	}
	for i := 0; i < expectedLength; i++ {
		if document.arena[fieldStart+i] != arena[expectedStart+i] {
			return false
		}
	}
	return true
}

func stateV3BoundedStringMatches(field stateV3BoundedField, document stateV3BoundedDocument, expected string) bool {
	fieldStart, fieldLength := int(field.start), int(field.length)
	if !field.present || fieldLength != len(expected) || fieldStart > len(document.arena) || fieldLength > len(document.arena)-fieldStart {
		return false
	}
	for i := range expected {
		if document.arena[fieldStart+i] != expected[i] {
			return false
		}
	}
	return true
}

func stateV3BoundedAbsent(field stateV3BoundedField) bool { return !field.present }
func stateV3BoundedNumber(field stateV3BoundedField, value int32) bool {
	return field.present && field.number == int64(value)
}

// StateV3BoundedArmMatchesClaimAcquire reports exact structural correlation.
func StateV3BoundedArmMatchesClaimAcquire(arm StateV3BoundedCoordinationArm, expected StateV3BoundedClaimAcquireArmExpectation) bool {
	d := arm.document
	return d.kind == stateV3BoundedArm &&
		stateV3BoundedExpectationMatches(d.fields[1], d, expected.arena, expected.claimPath) &&
		stateV3BoundedExpectationMatches(d.fields[3], d, expected.arena, expected.expectedHeadOID) &&
		stateV3BoundedExpectationMatches(d.fields[4], d, expected.arena, expected.intendedClaimSHA256) &&
		stateV3BoundedExpectationMatches(d.fields[5], d, expected.arena, expected.laneRef) &&
		stateV3BoundedStringMatches(d.fields[6], d, "claim_acquire") &&
		stateV3BoundedExpectationMatches(d.fields[7], d, expected.arena, expected.ref) &&
		stateV3BoundedExpectationMatches(d.fields[8], d, expected.arena, expected.releaseLine) &&
		stateV3BoundedExpectationMatches(d.fields[9], d, expected.arena, expected.requestKey) &&
		stateV3BoundedExpectationMatches(d.fields[10], d, expected.arena, expected.resolvedVersion) &&
		stateV3BoundedNumber(d.fields[0], expected.attempt) && stateV3BoundedAbsent(d.fields[2])
}

// StateV3BoundedArmMatchesClaimRelease reports exact structural correlation.
func StateV3BoundedArmMatchesClaimRelease(arm StateV3BoundedCoordinationArm, expected StateV3BoundedClaimReleaseArmExpectation) bool {
	d := arm.document
	return d.kind == stateV3BoundedArm &&
		stateV3BoundedExpectationMatches(d.fields[1], d, expected.arena, expected.claimPath) &&
		stateV3BoundedExpectationMatches(d.fields[2], d, expected.arena, expected.expectedClaimBlobOID) &&
		stateV3BoundedExpectationMatches(d.fields[3], d, expected.arena, expected.expectedHeadOID) &&
		stateV3BoundedExpectationMatches(d.fields[5], d, expected.arena, expected.laneRef) &&
		stateV3BoundedStringMatches(d.fields[6], d, "claim_release") &&
		stateV3BoundedExpectationMatches(d.fields[7], d, expected.arena, expected.ref) &&
		stateV3BoundedExpectationMatches(d.fields[8], d, expected.arena, expected.releaseLine) &&
		stateV3BoundedExpectationMatches(d.fields[9], d, expected.arena, expected.requestKey) &&
		stateV3BoundedExpectationMatches(d.fields[10], d, expected.arena, expected.resolvedVersion) &&
		stateV3BoundedNumber(d.fields[0], expected.attempt) && stateV3BoundedAbsent(d.fields[4])
}

// StateV3BoundedClaimMatchesActiveReservation reports exact document-local
// correlation for an active, reserved claim. It does not establish claim
// provenance, policy, reservation validity, or coordination lifecycle.
func StateV3BoundedClaimMatchesActiveReservation(claim StateV3BoundedCoordinationClaim, expected StateV3BoundedActiveClaimExpectation) bool {
	d := claim.document
	return d.kind == stateV3BoundedClaim &&
		stateV3BoundedNumber(d.fields[0], 1) &&
		stateV3BoundedExpectationMatches(d.fields[1], d, expected.arena, expected.command) &&
		(d.fields[2].present == expected.developmentVersionPresent) &&
		(!expected.developmentVersionPresent || stateV3BoundedExpectationMatches(d.fields[2], d, expected.arena, expected.developmentVersion)) &&
		stateV3BoundedExpectationMatches(d.fields[3], d, expected.arena, expected.laneExpectedHeadOID) &&
		stateV3BoundedExpectationMatches(d.fields[4], d, expected.arena, expected.laneRef) &&
		stateV3BoundedStringMatches(d.fields[5], d, "reserved") &&
		stateV3BoundedExpectationMatches(d.fields[6], d, expected.arena, expected.releaseLine) &&
		stateV3BoundedExpectationMatches(d.fields[7], d, expected.arena, expected.requestKey) &&
		stateV3BoundedExpectationMatches(d.fields[8], d, expected.arena, expected.requestSHA256) &&
		stateV3BoundedExpectationMatches(d.fields[9], d, expected.arena, expected.reservationSHA256) &&
		stateV3BoundedExpectationMatches(d.fields[10], d, expected.arena, expected.resolvedVersion) &&
		stateV3BoundedStringMatches(d.fields[11], d, "active") &&
		stateV3BoundedExpectationMatches(d.fields[12], d, expected.arena, expected.versionResolutionSHA256)
}

func stateV3BoundedObserved(document stateV3BoundedDocument, arena [stateV3BoundedCompactDocumentBytes]byte, oid stateV3BoundedExpectationSpan, attempts int32) bool {
	return attempts == 1 && stateV3BoundedNumber(document.response[0], 1) && stateV3BoundedStringMatches(document.response[1], document, "observed") && stateV3BoundedExpectationMatches(document.response[2], document, arena, oid)
}

// StateV3BoundedOutcomeMatchesClaimAcquire reports exact observed acquire correlation.
func StateV3BoundedOutcomeMatchesClaimAcquire(outcome StateV3BoundedCoordinationOutcome, expected StateV3BoundedClaimAcquireOutcomeExpectation) bool {
	d := outcome.document
	return d.kind == stateV3BoundedOutcome &&
		stateV3BoundedExpectationMatches(d.fields[0], d, expected.arena, expected.armBlobOID) && stateV3BoundedExpectationMatches(d.fields[1], d, expected.arena, expected.armCommitOID) && stateV3BoundedExpectationMatches(d.fields[2], d, expected.arena, expected.armPath) && stateV3BoundedExpectationMatches(d.fields[3], d, expected.arena, expected.armSHA256) && stateV3BoundedExpectationMatches(d.fields[4], d, expected.arena, expected.armTreeOID) && stateV3BoundedExpectationMatches(d.fields[7], d, expected.arena, expected.claimPath) && stateV3BoundedExpectationMatches(d.fields[11], d, expected.arena, expected.expectedHeadOID) && stateV3BoundedExpectationMatches(d.fields[12], d, expected.arena, expected.intendedClaimSHA256) && stateV3BoundedExpectationMatches(d.fields[13], d, expected.arena, expected.laneRef) && stateV3BoundedExpectationMatches(d.fields[14], d, expected.arena, expected.observedRefOID) && stateV3BoundedStringMatches(d.fields[15], d, "claim_acquire") && stateV3BoundedExpectationMatches(d.fields[16], d, expected.arena, expected.releaseLine) && stateV3BoundedExpectationMatches(d.fields[17], d, expected.arena, expected.requestKey) && stateV3BoundedExpectationMatches(d.fields[18], d, expected.arena, expected.requestSHA256) && stateV3BoundedExpectationMatches(d.fields[19], d, expected.arena, expected.resolvedVersion) && stateV3BoundedExpectationMatches(d.fields[21], d, expected.arena, expected.schemaVersion) && stateV3BoundedExpectationMatches(d.fields[22], d, expected.arena, expected.stateRef) && stateV3BoundedObserved(d, expected.arena, expected.responseOID, expected.responseAttempts) && stateV3BoundedAbsent(d.fields[5]) && stateV3BoundedAbsent(d.fields[6]) && stateV3BoundedAbsent(d.fields[8]) && stateV3BoundedAbsent(d.fields[9]) && stateV3BoundedAbsent(d.fields[10])
}

// StateV3BoundedOutcomeMatchesClaimRelease reports exact observed release correlation.
func StateV3BoundedOutcomeMatchesClaimRelease(outcome StateV3BoundedCoordinationOutcome, expected StateV3BoundedClaimReleaseOutcomeExpectation) bool {
	d := outcome.document
	return d.kind == stateV3BoundedOutcome &&
		stateV3BoundedExpectationMatches(d.fields[0], d, expected.arena, expected.armBlobOID) && stateV3BoundedExpectationMatches(d.fields[1], d, expected.arena, expected.armCommitOID) && stateV3BoundedExpectationMatches(d.fields[2], d, expected.arena, expected.armPath) && stateV3BoundedExpectationMatches(d.fields[3], d, expected.arena, expected.armSHA256) && stateV3BoundedExpectationMatches(d.fields[4], d, expected.arena, expected.armTreeOID) && stateV3BoundedExpectationMatches(d.fields[5], d, expected.arena, expected.claimBlobOID) && stateV3BoundedExpectationMatches(d.fields[6], d, expected.arena, expected.claimCommitOID) && stateV3BoundedExpectationMatches(d.fields[7], d, expected.arena, expected.claimPath) && stateV3BoundedExpectationMatches(d.fields[8], d, expected.arena, expected.claimSHA256) && stateV3BoundedExpectationMatches(d.fields[9], d, expected.arena, expected.claimTreeOID) && stateV3BoundedExpectationMatches(d.fields[10], d, expected.arena, expected.expectedClaimBlobOID) && stateV3BoundedExpectationMatches(d.fields[11], d, expected.arena, expected.expectedHeadOID) && stateV3BoundedExpectationMatches(d.fields[13], d, expected.arena, expected.laneRef) && stateV3BoundedExpectationMatches(d.fields[14], d, expected.arena, expected.observedRefOID) && stateV3BoundedStringMatches(d.fields[15], d, "claim_release") && stateV3BoundedExpectationMatches(d.fields[16], d, expected.arena, expected.releaseLine) && stateV3BoundedExpectationMatches(d.fields[17], d, expected.arena, expected.requestKey) && stateV3BoundedExpectationMatches(d.fields[18], d, expected.arena, expected.requestSHA256) && stateV3BoundedExpectationMatches(d.fields[19], d, expected.arena, expected.resolvedVersion) && stateV3BoundedExpectationMatches(d.fields[21], d, expected.arena, expected.schemaVersion) && stateV3BoundedExpectationMatches(d.fields[22], d, expected.arena, expected.stateRef) && stateV3BoundedObserved(d, expected.arena, expected.responseOID, expected.responseAttempts) && stateV3BoundedAbsent(d.fields[12])
}
