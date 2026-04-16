// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package internal

import (
	"fmt"
	"iter"
	"maps"
	"strings"

	"github.com/tinylib/msgp/msgp"
)

// metaMapHint is the initial capacity for the flat map m.
// A typical span carries "language" plus a handful of internal tags
// (_dd.base_service, runtime-id, etc.). 5 accommodates these without
// a rehash and matches the pre-refactor initMeta allocation profile.
const (
	// expectedEntries should be the count of tags known at construction time.
	expectedEntries = 5
	// loadFactor of 4/3 (≈ inverse of the standard 0.75 load factor) provides
	// ~33% slack so small overestimates don't trigger an immediate rehash.
	loadFactor  = 4 / 3
	metaMapHint = expectedEntries * loadFactor
)

var (
	_ msgp.Encodable = (*SpanMeta)(nil)
	_ msgp.Decodable = (*SpanMeta)(nil)
	_ msgp.Sizer     = (*SpanMeta)(nil)
)

// SpanMeta replaces a plain map[string]string for the Span.meta field.
// Promoted attributes (env, version, language) live in promotedAttrs and are
// excluded from the flat map m. The msgp codec and iterators merge both
// sources transparently so the wire format is unchanged.
//
// Set routes promoted keys to promotedAttrs (with copy-on-write) and others
// to the flat map. Promoted keys never appear in sm.m.
type SpanMeta struct {
	m             map[string]string
	promotedAttrs *SpanAttributes
}

// NewSpanMeta returns a SpanMeta initialized with shared promoted attrs (used during span creation).
func NewSpanMeta(promotedAttrs *SpanAttributes) SpanMeta {
	return SpanMeta{promotedAttrs: promotedAttrs}
}

// NewSpanMetaFromMap returns a SpanMeta pre-loaded with a flat map. Intended for test helpers.
func NewSpanMetaFromMap(m map[string]string) SpanMeta {
	return SpanMeta{m: m}
}

// IsZero reports whether the SpanMeta contains no entries (map or promoted).
// The msgp generator emits z.meta.IsZero() for the omitempty check.
func (sm *SpanMeta) IsZero() bool {
	return len(sm.m) == 0 && sm.promotedAttrs.Count() == 0
}

// ReplaceSharedAttrs replaces the current attrs pointer with next if it
// currently equals prev. Used by the tracer to upgrade a newly-created span
// from the base shared attrs to the main-service shared attrs.
func (sm *SpanMeta) ReplaceSharedAttrs(prev, next *SpanAttributes) {
	if sm.promotedAttrs == prev {
		sm.promotedAttrs = next
	}
}

// Normalize sets m and attrs to nil when they are empty so that a zero-length
// SpanMeta compares equal to a freshly-zeroed one. Intended for test helpers.
func (sm *SpanMeta) Normalize() {
	if len(sm.m) == 0 {
		sm.m = nil
	}
	if sm.promotedAttrs != nil && sm.promotedAttrs.Count() == 0 {
		sm.promotedAttrs = nil
	}
}

// ---------------------------------------------------------------------------
// Read methods
// ---------------------------------------------------------------------------

// Get returns the value for key. Promoted keys are checked in attrs first
// (fast array+bitmask path), then the flat map. Non-promoted keys go directly
// to the flat map.
func (sm *SpanMeta) Get(key string) (string, bool) {
	if IsPromotedKeyLen(len(key)) {
		if v, ok, handled := sm.getPromoted(key); handled {
			return v, ok
		}
	}
	if v, ok := sm.m[key]; ok {
		return v, ok
	}
	return "", false
}

// getPromoted is the slow path for Get when the key might be a promoted attribute.
//
//go:noinline
func (sm *SpanMeta) getPromoted(key string) (string, bool, bool) {
	ak, ok := AttrKeyForTag(key)
	if !ok {
		return "", false, false
	}
	v, found := sm.promotedAttrs.Get(ak)
	return v, found, true
}

// Has reports whether key is present.
func (sm *SpanMeta) Has(key string) bool {
	_, ok := sm.Get(key)
	return ok
}

// Attr returns a promoted attribute value by AttrKey. O(1) array index + bitmask.
func (sm *SpanMeta) Attr(key AttrKey) (string, bool) {
	return sm.promotedAttrs.Get(key)
}

// Env returns the value of the "env" promoted attribute.
func (sm *SpanMeta) Env() (string, bool) { return sm.promotedAttrs.Get(AttrEnv) }

// Version returns the value of the "version" promoted attribute.
func (sm *SpanMeta) Version() (string, bool) { return sm.promotedAttrs.Get(AttrVersion) }

// Language returns the value of the "language" promoted attribute.
func (sm *SpanMeta) Language() (string, bool) { return sm.promotedAttrs.Get(AttrLanguage) }

// Range calls fn for each flat-map entry. Promoted attrs are not in sm.m
// and are not yielded. Iteration stops if fn returns false.
func (sm *SpanMeta) Range(fn func(k, v string) bool) {
	for k, v := range sm.m {
		if !fn(k, v) {
			return
		}
	}
}

// ---------------------------------------------------------------------------
// Write methods
// ---------------------------------------------------------------------------

// Set sets key→value, routing promoted keys to attrs (with copy-on-write)
// and others to the flat map.
// +checklocksignore — called both at init time (no lock) and under lock.
func (sm *SpanMeta) Set(key, value string) {
	if IsPromotedKeyLen(len(key)) && sm.setPromoted(key, value) {
		return
	}
	if sm.m == nil {
		sm.initMap(key, value)
		return
	}
	sm.m[key] = value
}

// setPromoted is the slow path for Set when the key might be a promoted
// attribute. Returns true if the key was handled (set or no-op).
func (sm *SpanMeta) setPromoted(key, value string) bool {
	ak, ok := AttrKeyForTag(key)
	if !ok {
		return false
	}
	if sm.promotedAttrs != nil && sm.promotedAttrs.Has(ak) && sm.promotedAttrs.Val(ak) == value {
		return true // no-op: key is present and value already matches
	}
	sm.ensureAttrsLocal()
	sm.promotedAttrs.Set(ak, value)
	return true
}

// initMap allocates the flat map and inserts the first entry.
func (sm *SpanMeta) initMap(key, value string) {
	sm.m = make(map[string]string, metaMapHint)
	sm.m[key] = value
}

// ensureAttrsLocal guarantees attrs is a mutable, span-local instance.
// If attrs is nil a fresh one is allocated; if shared, it is cloned.
func (sm *SpanMeta) ensureAttrsLocal() {
	if sm.promotedAttrs == nil {
		sm.promotedAttrs = new(SpanAttributes)
		return
	}
	if sm.promotedAttrs.IsReadOnly() {
		sm.promotedAttrs = sm.promotedAttrs.Clone()
	}
}

// Delete removes key from both the flat map and (for promoted keys) attrs.
// +checklocksignore — called both at init time (no lock) and under lock.
//
// The length switch is intentionally duplicated from IsPromotedKeyLen rather
// than calling it. Inlining IsPromotedKeyLen (cost 11) into Delete raises
// Delete's budget from 73 to 81, crossing the 80-unit limit and preventing
// callers from inlining Delete. The direct switch keeps Delete at cost 73.
func (sm *SpanMeta) Delete(key string) {
	switch len(key) {
	case 3, 7, 8:
		sm.deleteSlow(key)
	default:
		delete(sm.m, key)
	}
}

// deleteSlow handles the promoted-key path for Delete.
func (sm *SpanMeta) deleteSlow(key string) {
	delete(sm.m, key)
	ak, ok := AttrKeyForTag(key)
	if !ok {
		return
	}
	if _, isSet := sm.promotedAttrs.Get(ak); !isSet {
		return
	}
	sm.ensureAttrsLocal()
	sm.promotedAttrs.Unset(ak)
}

// ---------------------------------------------------------------------------
// Counting / iteration
// ---------------------------------------------------------------------------

// Count returns the total number of distinct entries (flat map + promoted attrs).
func (sm *SpanMeta) Count() int {
	return len(sm.m) + sm.promotedAttrs.Count()
}

// AttrCount returns the number of promoted attrs currently set.
func (sm *SpanMeta) AttrCount() int {
	return sm.promotedAttrs.Count()
}

// Map returns a map containing meta entries.
//
// When full is true, promoted attrs (env, version, language) are merged into a
// new map (one allocation). Use this when the caller needs the complete view,
// e.g. CI visibility or test helpers.
//
// When full is false, the underlying flat map is returned directly
// (zero allocation). Promoted keys are excluded. Use this when the caller is
// known to not need promoted keys, e.g. the stats path reads span.kind,
// _dd.svc_src, HTTP/gRPC status codes, and peer tags — none of which are
// promoted attributes.
func (sm *SpanMeta) Map(full bool) map[string]string {
	if !full {
		return sm.m
	}
	n := sm.promotedAttrs.Count()
	if n == 0 {
		return sm.m
	}
	merged := make(map[string]string, len(sm.m)+n)
	maps.Copy(merged, sm.m)
	for _, d := range Defs {
		if sm.promotedAttrs.Has(d.Key) {
			merged[d.Name] = sm.promotedAttrs.Val(d.Key)
		}
	}
	return merged
}

// All returns an iterator over all entries. Flat-map entries are yielded first
// (in unspecified order), followed by promoted attributes.
// Returning false from yield stops iteration.
func (sm *SpanMeta) All() iter.Seq2[string, string] {
	return func(yield func(string, string) bool) {
		for k, v := range sm.m {
			if !yield(k, v) {
				return
			}
		}
		if sm.promotedAttrs == nil {
			return
		}
		for _, d := range Defs {
			if sm.promotedAttrs.Has(d.Key) {
				if !yield(d.Name, sm.promotedAttrs.Val(d.Key)) {
					return
				}
			}
		}
	}
}

// String returns a merged map representation (m + promoted attrs) for debug logging.
func (sm *SpanMeta) String() string {
	var b strings.Builder
	b.WriteString("map[")
	first := true
	for k, v := range sm.All() {
		if !first {
			b.WriteByte(' ')
		}
		first = false
		fmt.Fprintf(&b, "%s:%s", k, v)
	}
	b.WriteByte(']')
	return b.String()
}

// ---------------------------------------------------------------------------
// msgp codec
// ---------------------------------------------------------------------------

// EncodeMsg writes the map header and entries, combining the flat map and
// promoted attrs.
func (sm *SpanMeta) EncodeMsg(en *msgp.Writer) error {
	n := sm.promotedAttrs.Count()
	if err := en.WriteMapHeader(uint32(len(sm.m) + n)); err != nil {
		return msgp.WrapError(err, "Meta")
	}
	for k, v := range sm.m {
		if err := en.WriteString(k); err != nil {
			return msgp.WrapError(err, "Meta")
		}
		if err := en.WriteString(v); err != nil {
			return msgp.WrapError(err, "Meta", k)
		}
	}
	if n == 0 {
		return nil
	}
	var (
		v  string
		ok bool
	)
	for _, d := range Defs {
		if v, ok = sm.promotedAttrs.Get(d.Key); !ok {
			continue
		}
		if err := en.WriteString(d.Name); err != nil {
			return msgp.WrapError(err, "Meta")
		}
		if err := en.WriteString(v); err != nil {
			return msgp.WrapError(err, "Meta", d.Name)
		}
	}
	return nil
}

// DecodeMsg reads a msgp map into m. All keys — including promoted ones — go
// into the flat map so that no SpanAttributes allocation is needed on the
// decode path. attrs is only populated on the encode (span-creation) path.
func (sm *SpanMeta) DecodeMsg(dc *msgp.Reader) error {
	header, err := dc.ReadMapHeader()
	if err != nil {
		return msgp.WrapError(err, "Meta")
	}
	// Reuse sm.m if already allocated; otherwise allocate fresh pre-sized.
	if sm.m != nil {
		clear(sm.m)
	} else {
		sm.m = make(map[string]string, header)
	}
	for range header {
		key, err := dc.ReadString()
		if err != nil {
			return msgp.WrapError(err, "Meta")
		}
		val, err := dc.ReadString()
		if err != nil {
			return msgp.WrapError(err, "Meta", key)
		}
		sm.m[key] = val
	}
	return nil
}

// Msgsize returns an upper bound estimate of the serialized size, combining
// the flat map and promoted attrs.
func (sm *SpanMeta) Msgsize() int {
	size := msgp.MapHeaderSize
	for k, v := range sm.m {
		size += msgp.StringPrefixSize + len(k) + msgp.StringPrefixSize + len(v)
	}
	if n := sm.promotedAttrs.Count(); n > 0 {
		for _, d := range Defs {
			if v, ok := sm.promotedAttrs.Get(d.Key); ok {
				size += msgp.StringPrefixSize + len(d.Name) + msgp.StringPrefixSize + len(v)
			}
		}
	}
	return size
}
