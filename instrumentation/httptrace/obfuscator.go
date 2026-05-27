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

// sensitiveKeywordsByFirstByte groups sensitive keywords by their lower-cased ASCII
// first byte. Within each bucket, order is preserved from the original flat list:
// longer keywords precede their prefixes (e.g. "api_key_id" before "api_key",
// "pass_phrase"/"passphrase" before "pass"). Changing bucket order is safe; changing
// intra-bucket order may break prefix disambiguation.
var sensitiveKeywordsByFirstByte = [5]struct {
	first    byte
	keywords []string
}{
	{'a', []string{
		"api_key_id", "api_keyid", "api_key", "apikey_id", "apikeyid", "apikey",
		"access_key_id", "access_keyid", "access_key", "accesskey_id", "accesskeyid", "accesskey",
		"authentication", "authorization", "auth",
	}},
	{'c', []string{
		"consumer_id", "consumer_key", "consumer_secret", "consumerid", "consumerkey", "consumersecret",
	}},
	{'p', []string{
		"password", "passwd", "pword", "pwd",
		"pass_phrase", "passphrase", "pass",
		"private_key_id", "private_keyid", "private_key", "privatekey_id", "privatekeyid", "privatekey",
		"public_key_id", "public_keyid", "public_key", "publickey_id", "publickeyid", "publickey",
	}},
	{'s', []string{
		"secret",
		"secret_key_id", "secret_keyid", "secret_key", "secretkey_id", "secretkeyid", "secretkey",
		"signed", "signature", "sign",
	}},
	{'t', []string{
		"token",
	}},
}

// Per-byte ASCII class bitmasks for the obfuscator's character classifiers.
// Each bit covers ALL characters that belong to that class (not just the extras).
// Non-ASCII bytes are handled by the Unicode fold fallback in each classifier.
const (
	classAlpha    uint8 = 1 << 0 // [a-zA-Z]
	classDigit    uint8 = 1 << 1 // [0-9]
	classWord     uint8 = 1 << 2 // [a-zA-Z0-9_]
	classBearer   uint8 = 1 << 3 // [a-zA-Z0-9._-]
	classSSHBody  uint8 = 1 << 4 // [a-zA-Z0-9/+.]
	classJWTSeg   uint8 = 1 << 5 // [a-zA-Z0-9_=-]
	classJWTSig   uint8 = 1 << 6 // [a-zA-Z0-9_.+/=-]
	classAlphaNum uint8 = 1 << 7 // [a-zA-Z0-9]
)

// Regex-quantifier mirror constants. Each matches a fixed-length run in the
// original defaultQueryStringRegexp; keep these in sync if the regex changes.
const (
	shortTokenBodyLen = 13  // token(?::|%3A)[a-z0-9]{13}
	gitHubTokenLen    = 36  // gh[opsu]_[0-9a-zA-Z]{36}
	pemHyphenRun      = 5   // [\-]{5}
	sshRSAMinBody     = 100 // (?:[a-z0-9\/\.+]|%2F|%5C|%2B){100,}
)

// matcherStart is a 128-entry LUT indexed by ASCII byte value. Each bit marks
// a top-level matcher whose first character matches that byte (case-folded).
// The outer loop ANDs s[pos] against the LUT to skip matchers that cannot
// anchor here, instead of unconditionally invoking all seven. Non-ASCII bytes
// take the slow path (all matchers attempted via Unicode fold).
const (
	matcherSensitive  uint8 = 1 << 0 // sensitive keys: a/c/p/s/t
	matcherBearer     uint8 = 1 << 1 // bearer token: b
	matcherShortToken uint8 = 1 << 2 // token: t
	matcherGithub     uint8 = 1 << 3 // gh[opsu]_: g
	matcherJWT        uint8 = 1 << 4 // ey[I-L]…: e
	matcherPEM        uint8 = 1 << 5 // -----BEGIN…: -
	matcherSSHRSA     uint8 = 1 << 6 // ssh-rsa…: s
)

var matcherStart = func() [128]uint8 {
	var t [128]uint8
	setFolded := func(c byte, mask uint8) {
		t[c] |= mask
		if 'a' <= c && c <= 'z' {
			t[c-32] |= mask
		}
	}
	for _, c := range []byte{'a', 'c', 'p', 's', 't'} {
		setFolded(c, matcherSensitive)
	}
	setFolded('b', matcherBearer)
	setFolded('t', matcherShortToken)
	setFolded('g', matcherGithub)
	setFolded('e', matcherJWT)
	t['-'] |= matcherPEM
	setFolded('s', matcherSSHRSA)
	return t
}()

// asciiClass is a 128-entry lookup table indexed by ASCII byte value.
// It collapses the per-byte range checks in the six character classifiers
// into a single load + bitmask test.
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
	t['='] |= classJWTSeg | classJWTSig
	return t
}()

func emitObfuscated(b *strings.Builder, s string, last, pos, n int) int {
	if b.Len() == 0 {
		b.Grow(len(s))
	}
	b.WriteString(s[last:pos])
	b.WriteString("<redacted>")
	return pos + n
}

// obfuscateQueryStringDefault obfuscates s using the default query string
// obfuscation logic, equivalent to
// defaultQueryStringRegexp.ReplaceAllLiteralString(s, "<redacted>").
func obfuscateQueryStringDefault(s string) string {
	var b strings.Builder
	last := 0
	for pos := 0; pos < len(s); {
		c := s[pos]
		var mask uint8
		if c < utf8.RuneSelf {
			mask = matcherStart[c]
			if mask == 0 {
				pos++
				continue
			}
		} else {
			// Non-ASCII: any matcher may match via Unicode fold; try all.
			mask = 0xff
		}
		if mask&matcherSensitive != 0 {
			if n, ok := matchSensitiveKey(s, pos); ok {
				last = emitObfuscated(&b, s, last, pos, n)
				pos = last
				continue
			}
		}
		if mask&matcherBearer != 0 {
			if n, ok := matchBearerToken(s, pos); ok {
				last = emitObfuscated(&b, s, last, pos, n)
				pos = last
				continue
			}
		}
		if mask&matcherShortToken != 0 {
			if n, ok := matchShortToken(s, pos); ok {
				last = emitObfuscated(&b, s, last, pos, n)
				pos = last
				continue
			}
		}
		if mask&matcherGithub != 0 {
			if n, ok := matchGitHubToken(s, pos); ok {
				last = emitObfuscated(&b, s, last, pos, n)
				pos = last
				continue
			}
		}
		if mask&matcherJWT != 0 {
			if n, ok := matchJWT(s, pos); ok {
				last = emitObfuscated(&b, s, last, pos, n)
				pos = last
				continue
			}
		}
		if mask&matcherPEM != 0 {
			if n, ok := matchPEMPrivateKey(s, pos); ok {
				last = emitObfuscated(&b, s, last, pos, n)
				pos = last
				continue
			}
		}
		if mask&matcherSSHRSA != 0 {
			n, ok, skip := matchSSHRSAKey(s, pos)
			if ok {
				last = emitObfuscated(&b, s, last, pos, n)
				pos = last
				continue
			}
			if skip > 0 {
				pos += skip
				continue
			}
		}
		pos++
	}
	if b.Len() == 0 {
		return s
	}
	b.WriteString(s[last:])
	return b.String()
}

// matchSensitiveKey matches a sensitive keyword (password, api_key, …) followed
// by either =value or a JSON-style "key":"value" pair.
func matchSensitiveKey(s string, pos int) (int, bool) {
	if pos >= len(s) {
		return 0, false
	}
	c := s[pos]
	if c >= utf8.RuneSelf {
		// Non-ASCII: can fold to any keyword-initial letter; scan all buckets.
		// Cold path for RFC-3986 query strings.
		for i := range sensitiveKeywordsByFirstByte {
			for _, keyword := range sensitiveKeywordsByFirstByte[i].keywords {
				end, ok := matchFoldLiteral(s, pos, keyword)
				if !ok {
					continue
				}
				if suffixEnd, ok := matchSensitiveKeySuffix(s, end); ok {
					return suffixEnd - pos, true
				}
			}
		}
		return 0, false
	}
	var keywords []string
	switch toLowerASCII(c) {
	case 'a':
		keywords = sensitiveKeywordsByFirstByte[0].keywords
	case 'c':
		keywords = sensitiveKeywordsByFirstByte[1].keywords
	case 'p':
		keywords = sensitiveKeywordsByFirstByte[2].keywords
	case 's':
		keywords = sensitiveKeywordsByFirstByte[3].keywords
	case 't':
		keywords = sensitiveKeywordsByFirstByte[4].keywords
	default:
		return 0, false
	}
	for _, keyword := range keywords {
		end, ok := matchFoldLiteral(s, pos, keyword)
		if !ok {
			continue
		}
		if suffixEnd, ok := matchSensitiveKeySuffix(s, end); ok {
			return suffixEnd - pos, true
		}
	}
	return 0, false
}

func matchBearerToken(s string, pos int) (int, bool) {
	start := pos
	var ok bool
	if pos, ok = matchFoldLiteral(s, pos, "bearer"); !ok {
		return 0, false
	}
	spaceStart := pos
	pos = skipSpaces(s, pos)
	tokenEnd, ok := consumeBearerTokenChar(s, pos)
	if pos == spaceStart || !ok {
		return 0, false
	}
	// Quirk: the regexp has [a-z0-9._-] without a quantifier, so only one
	// token character after the spaces is redacted.
	return tokenEnd - start, true
}

// matchShortToken matches `token:` or `token%3A` followed by exactly 13 [a-z0-9] chars.
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

func matchJWT(s string, pos int) (int, bool) {
	start := pos
	var ok bool
	if pos, ok = consumeJWTHeader(s, pos); !ok {
		return 0, false
	}
	if pos, ok = consumeJWTSegment(s, pos); !ok {
		return 0, false
	}
	if pos >= len(s) || s[pos] != '.' {
		return 0, false
	}
	pos++
	if pos, ok = consumeJWTHeader(s, pos); !ok {
		return 0, false
	}
	if pos, ok = consumeJWTSegment(s, pos); !ok {
		return 0, false
	}
	if pos < len(s) && s[pos] == '.' {
		if end, ok := consumeJWTSignature(s, pos+1); ok {
			pos = end
		}
	}
	return pos - start, true
}

func matchPEMPrivateKey(s string, pos int) (int, bool) {
	start := pos
	var ok bool
	if pos, ok = matchHyphens(s, pos, pemHyphenRun); !ok {
		return 0, false
	}
	if pos, ok = matchFoldLiteral(s, pos, "BEGIN"); !ok {
		return 0, false
	}
	// Greedy regex: try longest label first. Forward-scan and keep the LAST
	// (longest) successful match instead of allocating a positions slice.
	labelPos, ok := consumePEMLabelChar(s, pos)
	if !ok {
		return 0, false
	}
	bestEnd := -1
	for {
		if afterKey, ok := matchPEMPrivateKeyLiteral(s, labelPos); ok {
			if end, ok := matchPEMBodyAndEnd(s, afterKey); ok && end > bestEnd {
				bestEnd = end
			}
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
	return bestEnd - start, true
}

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
	return matchPEMFinalPrivateKey(s, pos)
}

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

// matchSSHRSAKey returns (matchedLen, ok, safeSkip).
// On failure (ok=false), safeSkip is the number of bytes from pos that the
// outer loop can safely skip without missing any other match. This avoids
// re-scanning the entire key body when the key is too short.
//
// Safe-skip correctness table (matchers vs. SSH-RSA body charset [a-zA-Z0-9/+.]):
//   sensitive key (p/a/s/c/t)  — letters present, but suffix needs '='/'%3D'/'"'/':', none in body → safe
//   bearer                     — needs space after "bearer"; space not in body → safe
//   short-token                — needs ':' after "token"; ':' not in body → safe
//   github                     — needs '_' after gh[opsu]; '_' not in body → safe
//   JWT (eyJ…)                 — '.' in body charset; 'e'/'E' can start JWT → NOT safe; stop skip at e/E
//   PEM (-----)                — '-' not in body → safe
//   SSH-RSA itself             — '-' not in body → cannot re-anchor → safe
func matchSSHRSAKey(s string, pos int) (matchedLen int, ok bool, safeSkip int) {
	start := pos
	var matched bool
	if pos, matched = matchFoldLiteral(s, pos, "ssh-rsa"); !matched {
		return 0, false, 0
	}
	pos = skipSpaces(s, pos)
	safeEnd := pos
	count := 0
	for {
		next, ok := consumeSSHRSAKeyChar(s, pos)
		if !ok {
			break
		}
		// Stop safeEnd at 'e'/'E' (could anchor a JWT match) and at multi-byte
		// percent runs (kept conservative). s[pos]|32 ASCII-lowercases the
		// LUT-validated body char.
		if next == pos+1 && s[pos]|32 != 'e' {
			safeEnd = next
		}
		pos = next
		count++
	}
	if count < sshRSAMinBody {
		return 0, false, safeEnd - start
	}
	return pos - start, true, 0
}

func matchSensitiveKeySuffix(s string, pos int) (int, bool) {
	if end, ok := matchSensitiveKeyValue(s, pos); ok {
		return end, true
	}
	return matchSensitiveKeyJSON(s, pos)
}

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
	for pos < len(s) && s[pos] != '&' {
		pos++
	}
	return pos, true
}

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

func matchQuote(s string, pos int) (int, bool) {
	if pos < len(s) && s[pos] == '"' {
		return pos + 1, true
	}
	return matchFoldLiteral(s, pos, "%22")
}

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

func isSpace(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\f', '\r':
		return true
	default:
		return false
	}
}

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

func consumeJWTHeader(s string, pos int) (int, bool) {
	var ok bool
	if pos, ok = matchFoldLiteral(s, pos, "ey"); !ok {
		return 0, false
	}
	return consumeFoldedASCIISet(s, pos, "ijkl")
}

func consumeJWTSegment(s string, pos int) (int, bool) {
	start := pos
	for {
		next, ok := consumeJWTSegmentChar(s, pos)
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

func consumeJWTSegmentChar(s string, pos int) (int, bool) {
	if pos >= len(s) {
		return 0, false
	}
	c := s[pos]
	if c < utf8.RuneSelf {
		if asciiClass[c]&classJWTSeg != 0 {
			return pos + 1, true
		}
		if c == '%' {
			return matchFoldLiteral(s, pos, "%3D")
		}
		return 0, false
	}
	return foldsToLowerASCIILetter(s, pos)
}

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

func consumeSSHRSAKeyChar(s string, pos int) (int, bool) {
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
		if strings.IndexByte(chars, byte(folded)) >= 0 {
			return pos + width, true
		}
	}
	return 0, false
}

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

func isASCIILetter(c byte) bool {
	return ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z')
}

func toLowerASCII(c byte) byte {
	if 'A' <= c && c <= 'Z' {
		return c + 'a' - 'A'
	}
	return c
}
