// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package httptrace

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// sensitiveKeywords is the expanded list of the sensitive keys of alt 1 of
// defaultQueryStringRegexp:
//
//	(?:old[-_]?|new[-_]?)?p(?:ass)?w(?:or)?d(?:1|2)?
//	|pass(?:[-_]?phrase)?
//	|secret
//	|(?:api[-_]?|private[-_]?|public[-_]?|access[-_]?|secret[-_]?|app(?:lication)?[-_]?)key(?:[-_]?id)?
//	|token
//	|consumer[-_]?(?:id|key|secret)
//	|sign(?:ed|ature)?
//	|auth(?:entication|orization)?
//
// The order of the list has no effect on the result. A keyword only contains
// letters, digits, '-' and '_'. The key suffix of alt 1 starts with a space,
// '%', '=' or '"'. Thus, at a given position, when two keywords match, the end
// of the shorter keyword is followed by a keyword character, and only the
// longer keyword can be followed by a suffix. At most one keyword can match
// with a suffix.
var sensitiveKeywords = func() []string {
	seps := []string{"", "-", "_"}
	var kws []string
	// (?:old[-_]?|new[-_]?)?p(?:ass)?w(?:or)?d(?:1|2)?
	pwPrefixes := []string{""}
	for _, p := range []string{"old", "new"} {
		for _, sep := range seps {
			pwPrefixes = append(pwPrefixes, p+sep)
		}
	}
	for _, p := range pwPrefixes {
		for _, pw := range []string{"password", "passwd", "pword", "pwd"} {
			for _, d := range []string{"", "1", "2"} {
				kws = append(kws, p+pw+d)
			}
		}
	}
	// pass(?:[-_]?phrase)?
	kws = append(kws, "pass", "passphrase", "pass-phrase", "pass_phrase")
	// secret
	kws = append(kws, "secret")
	// (?:api|private|public|access|secret|app|application)[-_]?key(?:[-_]?id)?
	for _, p := range []string{"api", "private", "public", "access", "secret", "app", "application"} {
		for _, sep := range seps {
			for _, id := range []string{"", "id", "-id", "_id"} {
				kws = append(kws, p+sep+"key"+id)
			}
		}
	}
	// token
	kws = append(kws, "token")
	// consumer[-_]?(?:id|key|secret)
	for _, sep := range seps {
		for _, k := range []string{"id", "key", "secret"} {
			kws = append(kws, "consumer"+sep+k)
		}
	}
	// sign(?:ed|ature)?
	kws = append(kws, "sign", "signed", "signature")
	// auth(?:entication|orization)?
	kws = append(kws, "auth", "authentication", "authorization")
	return kws
}()

// keywordTrie is a trie of sensitiveKeywords. Node 0 is the root.
type keywordTrie []keywordTrieNode

type keywordTrieNode struct {
	// edges are the children of the node. A node has few children, thus a
	// linear search is fast.
	edges []keywordTrieEdge
	// terminal reports whether the path from the root to the node is a keyword.
	terminal bool
}

type keywordTrieEdge struct {
	c    byte // lowercase ASCII keyword character
	node int32
}

func newKeywordTrie(keywords []string) keywordTrie {
	t := keywordTrie{{}}
	for _, kw := range keywords {
		n := int32(0)
		for i := 0; i < len(kw); i++ {
			next := t.child(n, kw[i])
			if next < 0 {
				next = int32(len(t))
				t = append(t, keywordTrieNode{})
				t[n].edges = append(t[n].edges, keywordTrieEdge{c: kw[i], node: next})
			}
			n = next
		}
		t[n].terminal = true
	}
	return t
}

// child returns the child of node n for the lowercase ASCII character c, or -1.
func (t keywordTrie) child(n int32, c byte) int32 {
	for _, e := range t[n].edges {
		if e.c == c {
			return e.node
		}
	}
	return -1
}

var sensitiveKeywordTrie = newKeywordTrie(sensitiveKeywords)

// sensitiveKeywordStart reports, for each lowercase ASCII letter, whether a
// sensitive keyword starts with it.
var sensitiveKeywordStart = func() [26]bool {
	var t [26]bool
	for _, kw := range sensitiveKeywords {
		t[kw[0]-'a'] = true
	}
	return t
}()

// Per-byte ASCII class bitmasks for the obfuscator's character classifiers.
// Each bit covers ALL characters that belong to that class (not just the extras).
// Non-ASCII bytes are handled by the Unicode fold fallback in each classifier.
// The "alt N" annotations map to the alternatives in obfuscateQueryStringDefault.
const (
	classAlpha    uint8 = 1 << 0 // [a-zA-Z]              — PEM label chars (alt 6); base for derived classes
	classDigit    uint8 = 1 << 1 // [0-9]                 — base for derived classes
	classWord     uint8 = 1 << 2 // [a-zA-Z0-9_]          — \w; the JWT delimiter (alt 5) is not in [\w%-]
	classBearer   uint8 = 1 << 3 // [a-zA-Z0-9._-]        — bearer token body (alt 2), SSH key comment (alt 7)
	classSSHBody  uint8 = 1 << 4 // [a-zA-Z0-9/+.]        — SSH key body (alt 7)
	classJWTSeg   uint8 = 1 << 5 // [a-zA-Z0-9_-] ≡ [\w-]          — JWT header/payload segment char (alt 5)
	classJWTSig   uint8 = 1 << 6 // [a-zA-Z0-9_.+/=-] ≡ [\w.+/=-] — JWT signature char (alt 5)
	classAlphaNum uint8 = 1 << 7 // [a-zA-Z0-9]           — short token, GitHub token, ECDSA curve name (alts 3, 4, 7)
)

// Regex-quantifier mirror constants. Each matches a fixed-length run in
// defaultQueryStringRegexp; keep these in sync if the regex changes.
const (
	shortTokenBodyLen = 13  // token(?::|%3A)[a-z0-9]{13}
	gitHubTokenLen    = 36  // gh[opsu]_[0-9a-zA-Z]{36}
	pemHyphenRun      = 5   // -{5}
	sshKeyMinBody     = 100 // (?:[a-z0-9/.+]|%2F|%5C|%2B){100,}
)

// matcherStart is a 128-entry LUT indexed by ASCII byte value. Each bit marks
// a top-level matcher whose first character matches that byte (case-folded).
// The outer loop ANDs s[pos] against the LUT to skip matchers that cannot
// start here, instead of unconditionally invoking all seven. Non-ASCII bytes
// take the slow path (all matchers attempted via Unicode fold).
const (
	matcherSensitive  uint8 = 1 << 0 // optional quote ('"', '%22') or sensitive key: a/c/n/o/p/s/t
	matcherBearer     uint8 = 1 << 1 // bearer token: b
	matcherShortToken uint8 = 1 << 2 // token: t
	matcherGithub     uint8 = 1 << 3 // gh[opsu]_: g
	matcherJWT        uint8 = 1 << 4 // JWT delimiter: any ASCII byte not in [\w-] (also '%')
	matcherPEM        uint8 = 1 << 5 // -----BEGIN…: -
	matcherSSHKey     uint8 = 1 << 6 // ssh-…/ecdsa-…: s/e
)

var matcherStart = func() [128]uint8 {
	var t [128]uint8
	setFolded := func(c byte, mask uint8) {
		t[c] |= mask
		if 'a' <= c && c <= 'z' {
			t[c-32] |= mask
		}
	}
	for f, ok := range sensitiveKeywordStart {
		if ok {
			setFolded(byte('a'+f), matcherSensitive)
		}
	}
	t['"'] |= matcherSensitive
	t['%'] |= matcherSensitive
	setFolded('b', matcherBearer)
	setFolded('t', matcherShortToken)
	setFolded('g', matcherGithub)
	for c := range t {
		if c == '-' || isASCIIWord(byte(c)) {
			continue
		}
		t[c] |= matcherJWT
	}
	t['-'] |= matcherPEM
	setFolded('s', matcherSSHKey)
	setFolded('e', matcherSSHKey)
	return t
}()

// asciiClass is a 128-entry lookup table indexed by ASCII byte value.
// It collapses the per-byte range checks in the character classifiers into a
// single load + bitmask test.
var asciiClass = func() [128]uint8 {
	var t [128]uint8
	// Alpha: [a-zA-Z] — member of all classes that include alpha.
	allAlpha := classAlpha | classAlphaNum | classWord | classBearer | classSSHBody | classJWTSeg | classJWTSig
	for c := byte('a'); c <= 'z'; c++ {
		t[c] |= allAlpha
		t[c-32] |= allAlpha // A-Z
	}
	// Digits: [0-9] — member of all classes that include digits.
	allDigit := classDigit | classAlphaNum | classWord | classBearer | classSSHBody | classJWTSeg | classJWTSig
	for c := byte('0'); c <= '9'; c++ {
		t[c] |= allDigit
	}
	// Extra single chars per class.
	t['_'] |= classWord | classBearer | classJWTSeg | classJWTSig
	t['.'] |= classBearer | classSSHBody | classJWTSig
	t['-'] |= classBearer | classJWTSeg | classJWTSig
	t['/'] |= classSSHBody | classJWTSig
	t['+'] |= classSSHBody | classJWTSig
	t['='] |= classJWTSig
	return t
}()

// emitObfuscated writes s[last:pos+keep] and "<redacted>" to b, and returns
// the end of the match. The match is s[pos:pos+n]. Its first keep bytes are
// copied to the output (the JWT delimiter, see matchJWT); the other bytes are
// replaced.
func emitObfuscated(b *strings.Builder, s string, last, pos, n, keep int) int {
	if b.Len() == 0 {
		b.Grow(len(s))
	}
	b.WriteString(s[last : pos+keep])
	b.WriteString("<redacted>")
	return pos + n
}

// obfuscateQueryStringDefault obfuscates s using the default query string
// obfuscation logic. It is equivalent to
// defaultQueryStringRegexp.ReplaceAllString(s, defaultQueryStringReplacement):
// each match is replaced by "<redacted>", but the JWT delimiter (capture
// group 1) is kept.
//
// This is a hand-written state machine. It runs in linear time in the length
// of s. Each of the seven matcher branches implements one top-level
// alternative of defaultQueryStringRegexp, in the same order. The labels
// "alt 1 … alt 7" are used consistently across this file.
//
// Alt 1 — sensitive key + value (matcherSensitive → matchSensitiveKey):
//
//	(?i)(?:(?:"|%22)?)
//	(?:<keywords, see sensitiveKeywords>)
//	(?:(?:\s|%20)*(?:=|%3D)[^&]+                                      ← key=value
//	  |(?:"|%22)(?:\s|%20)*(?::|%3A)(?:\s|%20)*(?:"|%22)              ← JSON "key":"value"
//	   (?:%2[^2]|%[^2]|[^"%])+(?:"|%22))
//
// Alt 2 — bearer token (matcherBearer → matchBearerToken):
//
//	bearer(?:\s|%20)+[a-z0-9._\-]+
//
// Alt 3 — short token (matcherShortToken → matchShortToken):
//
//	token(?::|%3A)[a-z0-9]{13}
//
// Alt 4 — GitHub token (matcherGithub → matchGitHubToken):
//
//	gh[opsu]_[0-9a-zA-Z]{36}
//
// Alt 5 — JWT (matcherJWT → matchJWT):
//
//	(^|[^\w%-]|%[0-9a-f]{2})
//	ey[I-L][\w-]+(?:=|%3D)*\.ey[I-L][\w-]+(?:=|%3D)*
//	(?:\.(?:[\w.+/=-]|%3D|%2F|%2B)+)?
//
// Alt 6 — PEM private key (matcherPEM → matchPEMPrivateKey):
//
//	-{5}BEGIN(?:[a-z\s]|%20)+PRIVATE(?:\s|%20)KEY-{5}[^\-]+
//	-{5}END(?:[a-z\s]|%20)+PRIVATE(?:\s|%20)KEY(?:-{5})?(?:\n|%0A)?
//
// Alt 7 — SSH public key (matcherSSHKey → matchSSHKey):
//
//	(?:ssh-(?:rsa|dss)|ecdsa-[a-z0-9]+-[a-z0-9]+)(?:\s|%20|%09)+
//	(?:[a-z0-9/.+]|%2F|%5C|%2B){100,}(?:=|%3D)*(?:(?:\s|%20|%09)+[a-z0-9._-]+)?
//
// Note: "token" appears in both alt 1 (token=value) and alt 3 (token:…), so
// matcherStart['t'] sets both matcherSensitive and matcherShortToken bits.
//
// Like the regexp package, the outer loop only tries to start a match at the
// start of a UTF-8 sequence (an invalid byte is one sequence of length 1).
func obfuscateQueryStringDefault(s string) string {
	var b strings.Builder
	last := 0
	for pos := 0; pos < len(s); {
		c := s[pos]
		width := 1
		var mask uint8
		if c < utf8.RuneSelf {
			mask = matcherStart[c]
		} else {
			// Non-ASCII: any matcher may match via Unicode fold; try all.
			_, width = utf8.DecodeRuneInString(s[pos:])
			mask = 0xff
		}
		if pos == 0 {
			// The '^' JWT delimiter can only match at the start of s.
			mask |= matcherJWT
		}
		if mask == 0 {
			pos += width
			continue
		}
		if mask&matcherSensitive != 0 {
			if n, ok := matchSensitiveKey(s, pos); ok {
				last = emitObfuscated(&b, s, last, pos, n, 0)
				pos = last
				continue
			}
		}
		if mask&matcherBearer != 0 {
			if n, ok := matchBearerToken(s, pos); ok {
				last = emitObfuscated(&b, s, last, pos, n, 0)
				pos = last
				continue
			}
		}
		if mask&matcherShortToken != 0 {
			if n, ok := matchShortToken(s, pos); ok {
				last = emitObfuscated(&b, s, last, pos, n, 0)
				pos = last
				continue
			}
		}
		if mask&matcherGithub != 0 {
			if n, ok := matchGitHubToken(s, pos); ok {
				last = emitObfuscated(&b, s, last, pos, n, 0)
				pos = last
				continue
			}
		}
		if mask&matcherJWT != 0 {
			if n, keep, ok := matchJWT(s, pos); ok {
				last = emitObfuscated(&b, s, last, pos, n, keep)
				pos = last
				continue
			}
		}
		if mask&matcherPEM != 0 {
			if n, ok := matchPEMPrivateKey(s, pos); ok {
				last = emitObfuscated(&b, s, last, pos, n, 0)
				pos = last
				continue
			}
		}
		if mask&matcherSSHKey != 0 {
			if n, ok := matchSSHKey(s, pos); ok {
				last = emitObfuscated(&b, s, last, pos, n, 0)
				pos = last
				continue
			}
		}
		pos += width
	}
	if b.Len() == 0 {
		return s
	}
	b.WriteString(s[last:])
	return b.String()
}

// matchSensitiveKey implements alt 1 of defaultQueryStringRegexp: an optional
// quote, a sensitive keyword from sensitiveKeywords, and either a key=value
// suffix (matchSensitiveKeyValue) or a JSON "key":"value" suffix
// (matchSensitiveKeyJSON).
//
// The optional quote is greedy. When a quote is at pos, the keyword must
// follow it: no keyword starts with '"' or '%', thus the regex cannot match
// at pos without the quote.
//
// The keyword is found with sensitiveKeywordTrie. At each keyword end, the
// suffix is tried; see sensitiveKeywords for why the first success is the
// regex result.
func matchSensitiveKey(s string, pos int) (int, bool) {
	start := pos
	if pos < len(s) {
		switch s[pos] {
		case '"':
			pos++
		case '%':
			next, ok := matchFoldLiteral(s, pos, "%22")
			if !ok {
				return 0, false
			}
			pos = next
		}
	}
	node := int32(0)
	for pos < len(s) {
		c := s[pos]
		width := 1
		if c < utf8.RuneSelf {
			c = toLowerASCII(c)
		} else {
			// Non-ASCII: the rune matches a keyword letter if it folds to
			// it. Cold path for RFC-3986 query strings.
			var r rune
			r, width = utf8.DecodeRuneInString(s[pos:])
			c = foldToLowerASCIILetter(r)
			if c == 0 {
				return 0, false
			}
		}
		if node = sensitiveKeywordTrie.child(node, c); node < 0 {
			return 0, false
		}
		pos += width
		if sensitiveKeywordTrie[node].terminal {
			if suffixEnd, ok := matchSensitiveKeySuffix(s, pos); ok {
				return suffixEnd - start, true
			}
		}
	}
	return 0, false
}

// matchBearerToken implements alt 2: bearer(?:\s|%20)+[a-z0-9._\-]+
func matchBearerToken(s string, pos int) (int, bool) {
	start := pos
	var ok bool
	if pos, ok = matchFoldLiteral(s, pos, "bearer"); !ok {
		return 0, false
	}
	spaceStart := pos
	pos = skipSpaces(s, pos)
	if pos == spaceStart {
		return 0, false
	}
	if pos, ok = consumeBearerTokenChar(s, pos); !ok {
		return 0, false
	}
	for {
		next, ok := consumeBearerTokenChar(s, pos)
		if !ok {
			break
		}
		pos = next
	}
	return pos - start, true
}

// matchShortToken implements alt 3: token(?::|%3A)[a-z0-9]{13}
func matchShortToken(s string, pos int) (int, bool) {
	start := pos
	var ok bool
	if pos, ok = matchFoldLiteral(s, pos, "token"); !ok {
		return 0, false
	}
	if pos < len(s) && s[pos] == ':' {
		pos++
	} else if pos, ok = matchFoldLiteral(s, pos, "%3A"); !ok {
		return 0, false
	}
	for range shortTokenBodyLen {
		if pos, ok = consumeAlphaNumChar(s, pos); !ok {
			return 0, false
		}
	}
	return pos - start, true
}

// matchGitHubToken implements alt 4: gh[opsu]_[0-9a-zA-Z]{36}
func matchGitHubToken(s string, pos int) (int, bool) {
	start := pos
	var ok bool
	if pos, ok = matchFoldLiteral(s, pos, "gh"); !ok {
		return 0, false
	}
	if pos, ok = consumeFoldedASCIISet(s, pos, "opsu"); !ok {
		return 0, false
	}
	if pos >= len(s) || s[pos] != '_' {
		return 0, false
	}
	pos++
	for range gitHubTokenLen {
		if pos, ok = consumeAlphaNumChar(s, pos); !ok {
			return 0, false
		}
	}
	return pos - start, true
}

// matchJWT implements alt 5:
//
//	(^|[^\w%-]|%[0-9a-f]{2})
//	ey[I-L][\w-]+(?:=|%3D)*\.ey[I-L][\w-]+(?:=|%3D)*
//	(?:\.(?:[\w.+/=-]|%3D|%2F|%2B)+)?
//
// The match starts on a delimiter: the start of s, one character that is not
// in [\w%-], or a percent-escape. The delimiter is capture group 1 of
// defaultQueryStringRegexp; it is kept in the output. matchJWT returns the
// length n of the match and the length keep of the delimiter.
//
// Header and payload segments each begin with "ey" followed by one of [I-L]
// (the base64 encoding of the JSON byte '{'); the optional third segment is
// the signature.
//
// A JWT can only start after a delimiter, and [\w-] has no delimiter. Thus a
// failed attempt cannot be followed by an attempt that starts inside the same
// run of segment characters, and the total work stays linear.
func matchJWT(s string, pos int) (n, keep int, ok bool) {
	start := pos
	if pos == 0 {
		// '^' has the highest priority. It cannot conflict with the other
		// delimiters: 'e' is a word character.
		if end, ok := matchJWTBody(s, 0); ok {
			return end, 0, true
		}
	}
	var matched bool
	if pos, matched = consumeJWTDelimiter(s, pos); !matched {
		return 0, 0, false
	}
	keep = pos - start
	end, matched := matchJWTBody(s, pos)
	if !matched {
		return 0, 0, false
	}
	return end - start, keep, true
}

// matchJWTBody implements alt 5 after the delimiter:
// ey[I-L][\w-]+(?:=|%3D)*\.ey[I-L][\w-]+(?:=|%3D)*(?:\.(?:[\w.+/=-]|%3D|%2F|%2B)+)?
//
// [\w-], '=', "%3D" and '.' match different characters, thus there is no
// backtracking: each greedy run has only one possible length.
func matchJWTBody(s string, pos int) (int, bool) {
	var ok bool
	if pos, ok = consumeJWTSegment(s, pos); !ok {
		return 0, false
	}
	if pos >= len(s) || s[pos] != '.' {
		return 0, false
	}
	if pos, ok = consumeJWTSegment(s, pos+1); !ok {
		return 0, false
	}
	if pos < len(s) && s[pos] == '.' {
		if end, ok := consumeJWTSignature(s, pos+1); ok {
			pos = end
		}
	}
	return pos, true
}

// consumeJWTDelimiter implements [^\w%-]|%[0-9a-f]{2} — the JWT delimiter (alt 5).
// With (?i), [\w] also has the non-ASCII runes that fold to an ASCII letter
// (U+212A KELVIN SIGN, U+017F LATIN SMALL LETTER LONG S); thus they are not
// delimiters. An invalid UTF-8 byte decodes to U+FFFD, which is a delimiter.
func consumeJWTDelimiter(s string, pos int) (int, bool) {
	if pos >= len(s) {
		return 0, false
	}
	c := s[pos]
	if c >= utf8.RuneSelf {
		if _, ok := foldsToLowerASCIILetter(s, pos); ok {
			return 0, false
		}
		_, width := utf8.DecodeRuneInString(s[pos:])
		return pos + width, true
	}
	if c == '%' {
		if len(s)-pos < 3 || !isHexDigit(s[pos+1]) || !isHexDigit(s[pos+2]) {
			return 0, false
		}
		return pos + 3, true
	}
	if c == '-' || isASCIIWord(c) {
		return 0, false
	}
	return pos + 1, true
}

// consumeJWTSegment implements ey[I-L][\w-]+(?:=|%3D)* — a JWT header or payload segment (alt 5).
func consumeJWTSegment(s string, pos int) (int, bool) {
	var ok bool
	if pos, ok = matchFoldLiteral(s, pos, "ey"); !ok {
		return 0, false
	}
	// [I-L] case-folds to [i-l]; the set {"i","j","k","l"} covers the base64
	// encodings of the four possible first bytes of a JSON object: 0x7B = '{'.
	if pos, ok = consumeFoldedASCIISet(s, pos, "ijkl"); !ok {
		return 0, false
	}
	if pos, ok = consumeJWTSegmentChar(s, pos); !ok {
		return 0, false
	}
	for {
		next, ok := consumeJWTSegmentChar(s, pos)
		if !ok {
			break
		}
		pos = next
	}
	for {
		if pos < len(s) && s[pos] == '=' {
			pos++
			continue
		}
		next, ok := matchFoldLiteral(s, pos, "%3D")
		if !ok {
			break
		}
		pos = next
	}
	return pos, true
}

// matchPEMPrivateKey implements alt 6:
// -{5}BEGIN(?:[a-z\s]|%20)+PRIVATE(?:\s|%20)KEY-{5}[^\-]+
// -{5}END(?:[a-z\s]|%20)+PRIVATE(?:\s|%20)KEY(?:-{5})?(?:\n|%0A)?
// The "PRIVATE KEY" substring may appear anywhere inside the PEM label (e.g.
// "ENCRYPTED PRIVATE KEY"), so the scanner advances through label chars and
// records the last position where "PRIVATE KEY" matches — implementing the
// greedy semantics of the regexp.
func matchPEMPrivateKey(s string, pos int) (int, bool) {
	start := pos
	var ok bool
	if pos, ok = matchHyphens(s, pos, pemHyphenRun); !ok {
		return 0, false
	}
	if pos, ok = matchFoldLiteral(s, pos, "BEGIN"); !ok {
		return 0, false
	}
	// Scan label chars for "PRIVATE KEY", attempt body+end from each hit.
	// Because the label charset excludes '-', only the occurrence directly
	// before the fence "-----" can produce a successful body+end match; all
	// earlier hits fail matchHyphens in O(1).  We must continue (not break)
	// after a failed hit to reach that final occurrence.
	labelPos, ok := consumePEMLabelChar(s, pos)
	if !ok {
		return 0, false
	}
	for {
		if afterKey, ok := matchPEMPrivateKeyLiteral(s, labelPos); ok {
			if end, ok := matchPEMBodyAndEnd(s, afterKey); ok {
				return end - start, true
			}
			// afterKey was not at the fence; keep scanning for a later hit.
		}
		next, ok := consumePEMLabelChar(s, labelPos)
		if !ok {
			break
		}
		labelPos = next
	}
	return 0, false
}

// matchPEMBodyAndEnd implements -{5}[^\-]+-{5}END… — PEM body and footer (alt 6).
func matchPEMBodyAndEnd(s string, pos int) (int, bool) {
	var ok bool
	if pos, ok = matchHyphens(s, pos, pemHyphenRun); !ok {
		return 0, false
	}
	if pos, ok = consumeNonHyphenRun(s, pos); !ok {
		return 0, false
	}
	if pos, ok = matchHyphens(s, pos, pemHyphenRun); !ok {
		return 0, false
	}
	if pos, ok = matchFoldLiteral(s, pos, "END"); !ok {
		return 0, false
	}
	if pos, ok = matchPEMFinalPrivateKey(s, pos); !ok {
		return 0, false
	}
	// (?:-{5})?(?:\n|%0A)? — both are optional and greedy.
	if next, ok := matchHyphens(s, pos, pemHyphenRun); ok {
		pos = next
	}
	if pos < len(s) && s[pos] == '\n' {
		pos++
	} else if next, ok := matchFoldLiteral(s, pos, "%0A"); ok {
		pos = next
	}
	return pos, true
}

// matchPEMFinalPrivateKey implements (?:[a-z\s]|%20)+PRIVATE(?:\s|%20)KEY in the PEM footer (alt 6).
// The greedy label selects the last "PRIVATE KEY" in the label run. The
// literal starts with 'P', and 'P' only occurs at its start, thus the last hit
// also has the largest end.
func matchPEMFinalPrivateKey(s string, pos int) (int, bool) {
	labelPos, ok := consumePEMLabelChar(s, pos)
	if !ok {
		return 0, false
	}
	bestEnd := -1
	for {
		if end, ok := matchPEMPrivateKeyLiteral(s, labelPos); ok && end > bestEnd {
			bestEnd = end
		}
		next, ok := consumePEMLabelChar(s, labelPos)
		if !ok {
			break
		}
		labelPos = next
	}
	if bestEnd < 0 {
		return 0, false
	}
	return bestEnd, true
}

// matchPEMPrivateKeyLiteral implements PRIVATE(?:\s|%20)KEY — shared by the BEGIN and END label scanners (alt 6).
func matchPEMPrivateKeyLiteral(s string, pos int) (int, bool) {
	var ok bool
	if pos, ok = matchFoldLiteral(s, pos, "PRIVATE"); !ok {
		return 0, false
	}
	if pos, ok = consumeSpaceOrPct20(s, pos); !ok {
		return 0, false
	}
	return matchFoldLiteral(s, pos, "KEY")
}

// matchSSHKey implements alt 7:
//
//	(?:ssh-(?:rsa|dss)|ecdsa-[a-z0-9]+-[a-z0-9]+)(?:\s|%20|%09)+
//	(?:[a-z0-9/.+]|%2F|%5C|%2B){100,}(?:=|%3D)*(?:(?:\s|%20|%09)+[a-z0-9._-]+)?
//
// Each greedy run matches characters that the next element cannot match, thus
// there is no backtracking. The key type always has a '-', and the separator
// and the body have no '-'. Thus a failed attempt cannot be followed by an
// attempt that starts inside its separator or body, and the total work stays
// linear.
func matchSSHKey(s string, pos int) (int, bool) {
	start := pos
	var ok bool
	if pos, ok = matchSSHKeyType(s, pos); !ok {
		return 0, false
	}
	if pos, ok = skipSSHSpaces(s, pos); !ok {
		return 0, false
	}
	count := 0
	for {
		next, ok := consumeSSHKeyChar(s, pos)
		if !ok {
			break
		}
		pos = next
		count++
	}
	if count < sshKeyMinBody {
		return 0, false
	}
	// (?:=|%3D)* — base64 padding.
	for {
		if pos < len(s) && s[pos] == '=' {
			pos++
			continue
		}
		next, ok := matchFoldLiteral(s, pos, "%3D")
		if !ok {
			break
		}
		pos = next
	}
	// (?:(?:\s|%20|%09)+[a-z0-9._-]+)? — optional key comment.
	if afterSpaces, ok := skipSSHSpaces(s, pos); ok {
		if end, ok := consumeBearerTokenChar(s, afterSpaces); ok {
			for {
				next, ok := consumeBearerTokenChar(s, end)
				if !ok {
					break
				}
				end = next
			}
			pos = end
		}
	}
	return pos - start, true
}

// matchSSHKeyType implements ssh-(?:rsa|dss)|ecdsa-[a-z0-9]+-[a-z0-9]+ (alt 7).
func matchSSHKeyType(s string, pos int) (int, bool) {
	if next, ok := matchFoldLiteral(s, pos, "ssh-"); ok {
		if end, ok := matchFoldLiteral(s, next, "rsa"); ok {
			return end, true
		}
		return matchFoldLiteral(s, next, "dss")
	}
	var ok bool
	if pos, ok = matchFoldLiteral(s, pos, "ecdsa-"); !ok {
		return 0, false
	}
	if pos, ok = consumeAlphaNumRun(s, pos); !ok {
		return 0, false
	}
	if pos >= len(s) || s[pos] != '-' {
		return 0, false
	}
	return consumeAlphaNumRun(s, pos+1)
}

// skipSSHSpaces implements (?:\s|%20|%09)+ — one or more spaces, encoded spaces or encoded tabs (alt 7).
func skipSSHSpaces(s string, pos int) (int, bool) {
	start := pos
	for pos < len(s) {
		if isSpace(s[pos]) {
			pos++
			continue
		}
		if next, ok := matchFoldLiteral(s, pos, "%20"); ok {
			pos = next
			continue
		}
		if next, ok := matchFoldLiteral(s, pos, "%09"); ok {
			pos = next
			continue
		}
		break
	}
	return pos, pos > start
}

// matchSensitiveKeySuffix tries both suffixes of alt 1: key=value, then "key":"value".
func matchSensitiveKeySuffix(s string, pos int) (int, bool) {
	if end, ok := matchSensitiveKeyValue(s, pos); ok {
		return end, true
	}
	return matchSensitiveKeyJSON(s, pos)
}

// matchSensitiveKeyValue implements the first suffix form of alt 1: (?:\s|%20)*(?:=|%3D)[^&]+
func matchSensitiveKeyValue(s string, pos int) (int, bool) {
	pos = skipSpaces(s, pos)
	var ok bool
	if pos < len(s) && s[pos] == '=' {
		pos++
	} else if pos, ok = matchFoldLiteral(s, pos, "%3D"); !ok {
		return 0, false
	}
	if pos >= len(s) || s[pos] == '&' {
		return 0, false
	}
	if i := strings.IndexByte(s[pos:], '&'); i >= 0 {
		return pos + i, true
	}
	return len(s), true
}

// matchSensitiveKeyJSON implements the second suffix form of alt 1:
// (?:"|%22)(?:\s|%20)*(?::|%3A)(?:\s|%20)*(?:"|%22)(?:%2[^2]|%[^2]|[^"%])+(?:"|%22)
func matchSensitiveKeyJSON(s string, pos int) (int, bool) {
	var ok bool
	if pos, ok = matchQuote(s, pos); !ok {
		return 0, false
	}
	pos = skipSpaces(s, pos)
	if pos < len(s) && s[pos] == ':' {
		pos++
	} else if pos, ok = matchFoldLiteral(s, pos, "%3A"); !ok {
		return 0, false
	}
	pos = skipSpaces(s, pos)
	if pos, ok = matchQuote(s, pos); !ok {
		return 0, false
	}
	valueStart := pos
	pos = consumeJSONValue(s, pos)
	if pos == valueStart {
		return 0, false
	}
	if pos, ok = matchQuote(s, pos); !ok {
		return 0, false
	}
	return pos, true
}

// matchQuote implements (?:"|%22) — a literal or percent-encoded double-quote (alt 1).
func matchQuote(s string, pos int) (int, bool) {
	if pos < len(s) && s[pos] == '"' {
		return pos + 1, true
	}
	return matchFoldLiteral(s, pos, "%22")
}

// skipSpaces implements (?:\s|%20)* — zero or more spaces or percent-encoded spaces.
func skipSpaces(s string, pos int) int {
	for pos < len(s) {
		if isSpace(s[pos]) {
			pos++
			continue
		}
		if next, ok := matchFoldLiteral(s, pos, "%20"); ok {
			pos = next
			continue
		}
		return pos
	}
	return pos
}

// matchHyphens implements -{n} — exactly n consecutive hyphens (PEM fence, alt 6).
func matchHyphens(s string, pos int, n int) (int, bool) {
	if len(s)-pos < n {
		return 0, false
	}
	for i := range n {
		if s[pos+i] != '-' {
			return 0, false
		}
	}
	return pos + n, true
}

// isSpace reports whether c is in \s as defined by the regexp package: [\t\n\f\r ].
func isSpace(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\f', '\r':
		return true
	default:
		return false
	}
}

// consumePEMLabelChar implements [a-z\s]|%20 — one character of a PEM label (alt 6).
func consumePEMLabelChar(s string, pos int) (int, bool) {
	if next, ok := consumeAlphaChar(s, pos); ok {
		return next, true
	}
	if next, ok := consumeSpaceOrPct20(s, pos); ok {
		return next, true
	}
	return 0, false
}

func consumeSpaceOrPct20(s string, pos int) (int, bool) {
	if pos < len(s) && isSpace(s[pos]) {
		return pos + 1, true
	}
	return matchFoldLiteral(s, pos, "%20")
}

// consumeNonHyphenRun implements [^\-]+ — the PEM body between the BEGIN and END fences (alt 6).
func consumeNonHyphenRun(s string, pos int) (int, bool) {
	i := strings.IndexByte(s[pos:], '-')
	if i == 0 {
		return 0, false
	}
	if i < 0 {
		return len(s), true
	}
	return pos + i, true
}

// foldsToLowerASCIILetter is the non-ASCII slow path shared by every per-byte
// classifier whose ASCII members are exactly [a-zA-Z]: a multi-byte rune
// matches iff some SimpleFold of it lands on an ASCII lowercase letter.
// Callers must already have taken the ASCII fast path; this helper decodes
// s[pos:] as UTF-8 unconditionally.
func foldsToLowerASCIILetter(s string, pos int) (int, bool) {
	r, width := utf8.DecodeRuneInString(s[pos:])
	for folded := unicode.SimpleFold(r); folded != r; folded = unicode.SimpleFold(folded) {
		if 'a' <= folded && folded <= 'z' {
			return pos + width, true
		}
	}
	return 0, false
}

// foldToLowerASCIILetter returns the ASCII lowercase letter that the
// non-ASCII rune r folds to, or 0. A rune folds to at most one ASCII
// lowercase letter.
func foldToLowerASCIILetter(r rune) byte {
	for folded := unicode.SimpleFold(r); folded != r; folded = unicode.SimpleFold(folded) {
		if 'a' <= folded && folded <= 'z' {
			return byte(folded)
		}
	}
	return 0
}

// consumeJWTSegmentChar implements [\w-] — one character of a JWT segment (alt 5).
func consumeJWTSegmentChar(s string, pos int) (int, bool) {
	if pos >= len(s) {
		return 0, false
	}
	c := s[pos]
	if c < utf8.RuneSelf {
		if asciiClass[c]&classJWTSeg != 0 {
			return pos + 1, true
		}
		return 0, false
	}
	return foldsToLowerASCIILetter(s, pos)
}

// consumeJWTSignature implements (?:[\w.+/=-]|%3D|%2F|%2B)+ — the JWT signature (alt 5).
func consumeJWTSignature(s string, pos int) (int, bool) {
	start := pos
	for {
		next, ok := consumeJWTSignatureChar(s, pos)
		if !ok {
			break
		}
		pos = next
	}
	if pos == start {
		return 0, false
	}
	return pos, true
}

func consumeJWTSignatureChar(s string, pos int) (int, bool) {
	if pos >= len(s) {
		return 0, false
	}
	c := s[pos]
	if c < utf8.RuneSelf {
		if asciiClass[c]&classJWTSig != 0 {
			return pos + 1, true
		}
		if c == '%' {
			if next, ok := matchFoldLiteral(s, pos, "%3D"); ok {
				return next, true
			}
			if next, ok := matchFoldLiteral(s, pos, "%2F"); ok {
				return next, true
			}
			return matchFoldLiteral(s, pos, "%2B")
		}
		return 0, false
	}
	return foldsToLowerASCIILetter(s, pos)
}

// consumeBearerTokenChar implements [a-z0-9._\-] — the bearer token (alt 2) and SSH key comment (alt 7) charset.
func consumeBearerTokenChar(s string, pos int) (int, bool) {
	if pos >= len(s) {
		return 0, false
	}
	c := s[pos]
	if c < utf8.RuneSelf {
		if asciiClass[c]&classBearer != 0 {
			return pos + 1, true
		}
		return 0, false
	}
	return foldsToLowerASCIILetter(s, pos)
}

// consumeSSHKeyChar implements [a-z0-9/.+]|%2F|%5C|%2B — one repetition of the SSH key body (alt 7).
func consumeSSHKeyChar(s string, pos int) (int, bool) {
	if pos >= len(s) {
		return 0, false
	}
	c := s[pos]
	if c < utf8.RuneSelf {
		if asciiClass[c]&classSSHBody != 0 {
			return pos + 1, true
		}
		if c == '%' {
			if next, ok := matchFoldLiteral(s, pos, "%2F"); ok {
				return next, true
			}
			if next, ok := matchFoldLiteral(s, pos, "%5C"); ok {
				return next, true
			}
			return matchFoldLiteral(s, pos, "%2B")
		}
		return 0, false
	}
	return foldsToLowerASCIILetter(s, pos)
}

func consumeAlphaChar(s string, pos int) (int, bool) {
	if pos >= len(s) {
		return 0, false
	}
	c := s[pos]
	if c < utf8.RuneSelf {
		if asciiClass[c]&classAlpha != 0 {
			return pos + 1, true
		}
		return 0, false
	}
	return foldsToLowerASCIILetter(s, pos)
}

func consumeAlphaNumChar(s string, pos int) (int, bool) {
	if pos >= len(s) {
		return 0, false
	}
	c := s[pos]
	if c < utf8.RuneSelf {
		if asciiClass[c]&classAlphaNum != 0 {
			return pos + 1, true
		}
		return 0, false
	}
	return foldsToLowerASCIILetter(s, pos)
}

// consumeAlphaNumRun implements [a-z0-9]+ (alt 7).
func consumeAlphaNumRun(s string, pos int) (int, bool) {
	var ok bool
	if pos, ok = consumeAlphaNumChar(s, pos); !ok {
		return 0, false
	}
	for {
		next, ok := consumeAlphaNumChar(s, pos)
		if !ok {
			return pos, true
		}
		pos = next
	}
}

func consumeFoldedASCIISet(s string, pos int, chars string) (int, bool) {
	if pos >= len(s) {
		return 0, false
	}
	if s[pos] < utf8.RuneSelf {
		if strings.IndexByte(chars, toLowerASCII(s[pos])) >= 0 {
			return pos + 1, true
		}
		return 0, false
	}
	r, width := utf8.DecodeRuneInString(s[pos:])
	for folded := unicode.SimpleFold(r); folded != r; folded = unicode.SimpleFold(folded) {
		if folded < utf8.RuneSelf && strings.IndexByte(chars, byte(folded)) >= 0 {
			return pos + width, true
		}
	}
	return 0, false
}

// consumeJSONValue implements (?:%2[^2]|%[^2]|[^"%])+ — JSON value body between the quotes (alt 1).
func consumeJSONValue(s string, pos int) int {
	for pos < len(s) {
		// The value regexp is (%2[^2]|%[^2]|[^"%])+. The order is
		// observable for inputs such as %20 and %2", so keep it verbatim.
		if next, ok := consumeJSONValuePct2(s, pos); ok {
			pos = next
			continue
		}
		if next, ok := consumeJSONValuePct(s, pos); ok {
			pos = next
			continue
		}
		r, width := utf8.DecodeRuneInString(s[pos:])
		if r == '"' || r == '%' {
			return pos
		}
		pos += width
	}
	return pos
}

func consumeJSONValuePct2(s string, pos int) (int, bool) {
	next, ok := matchFoldLiteral(s, pos, "%2")
	if !ok || next >= len(s) {
		return 0, false
	}
	r, width := utf8.DecodeRuneInString(s[next:])
	if r == '2' {
		return 0, false
	}
	return next + width, true
}

func consumeJSONValuePct(s string, pos int) (int, bool) {
	if pos >= len(s) || s[pos] != '%' || pos+1 >= len(s) {
		return 0, false
	}
	r, width := utf8.DecodeRuneInString(s[pos+1:])
	if r == '2' {
		return 0, false
	}
	return pos + 1 + width, true
}

func matchFoldLiteral(s string, pos int, lit string) (int, bool) {
	for i := 0; i < len(lit); i++ {
		if pos >= len(s) {
			return 0, false
		}
		want := lit[i]
		if isASCIILetter(want) {
			if s[pos] < utf8.RuneSelf {
				if toLowerASCII(s[pos]) != toLowerASCII(want) {
					return 0, false
				}
				pos++
				continue
			}
			r, width := utf8.DecodeRuneInString(s[pos:])
			if !equalFoldASCII(r, toLowerASCII(want)) {
				return 0, false
			}
			pos += width
			continue
		}
		if s[pos] != want {
			return 0, false
		}
		pos++
	}
	return pos, true
}

// equalFoldASCII reports whether rune r Unicode-case-folds to the ASCII
// lowercase letter lower. It exists because the stdlib fold functions operate
// on strings (strings.EqualFold) or full runes (unicode.SimpleFold) but
// expose no zero-allocation "does this rune fold to this specific ASCII byte"
// primitive. strings.EqualFold("x", string(r)) would allocate; calling it at
// every character position of a hot scanner path is too costly.
func equalFoldASCII(r rune, lower byte) bool {
	want := rune(lower)
	for folded := r; ; folded = unicode.SimpleFold(folded) {
		if folded == want {
			return true
		}
		next := unicode.SimpleFold(folded)
		if next == r {
			return false
		}
	}
}

// isASCIILetter reports whether c is an ASCII letter [a-zA-Z]. It exists
// because unicode.IsLetter takes a rune and traverses the full Unicode letter
// table; this state machine only needs to classify raw bytes from a query
// string, so the cheaper range check suffices and avoids a rune conversion.
func isASCIILetter(c byte) bool {
	return ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z')
}

// isASCIIWord reports whether c is in [0-9A-Za-z_] (\w for ASCII).
func isASCIIWord(c byte) bool {
	return isASCIILetter(c) || ('0' <= c && c <= '9') || c == '_'
}

// isHexDigit reports whether c is in [0-9a-fA-F]. With (?i), no non-ASCII
// rune folds to [a-f].
func isHexDigit(c byte) bool {
	return ('0' <= c && c <= '9') || ('a' <= c && c <= 'f') || ('A' <= c && c <= 'F')
}

// toLowerASCII returns the ASCII lowercase form of c. It exists because
// unicode.ToLower operates on runes and bytes.ToLower/strings.ToLower operate
// on slices/strings (allocating). The state machine compares individual bytes
// from a query string, all of which are ASCII in the fast path, so a single
// branch is both correct and allocation-free.
func toLowerASCII(c byte) byte {
	if 'A' <= c && c <= 'Z' {
		return c + 'a' - 'A'
	}
	return c
}
