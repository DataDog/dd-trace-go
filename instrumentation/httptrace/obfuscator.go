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

// sensitiveByFirstLetter groups sensitive keywords by their ASCII first-letter
// offset ('a'->0, …, 'z'->25). Letters that can't anchor a sensitive keyword
// (i.e. anything other than a/c/p/s/t) leave their cell nil. Within each bucket
// the order is significant: longer keywords precede their prefixes (e.g.
// "api_key_id" before "api_key", "pass_phrase"/"passphrase" before "pass") so
// the suffix-matching pass picks the longest viable keyword before a shorter
// prefix accidentally short-circuits the search.
var sensitiveByFirstLetter = [26][]string{
	'a' - 'a': {
		"api_key_id", "api_keyid", "api_key", "apikey_id", "apikeyid", "apikey",
		"access_key_id", "access_keyid", "access_key", "accesskey_id", "accesskeyid", "accesskey",
		"authentication", "authorization", "auth",
	},
	'c' - 'a': {
		"consumer_id", "consumer_key", "consumer_secret", "consumerid", "consumerkey", "consumersecret",
	},
	'p' - 'a': {
		"password", "passwd", "pword", "pwd",
		"pass_phrase", "passphrase", "pass",
		"private_key_id", "private_keyid", "private_key", "privatekey_id", "privatekeyid", "privatekey",
		"public_key_id", "public_keyid", "public_key", "publickey_id", "publickeyid", "publickey",
	},
	's' - 'a': {
		"secret",
		"secret_key_id", "secret_keyid", "secret_key", "secretkey_id", "secretkeyid", "secretkey",
		"signed", "signature", "sign",
	},
	't' - 'a': {
		"token",
	},
}

// Per-byte ASCII class bitmasks for the obfuscator's character classifiers.
// Each bit covers ALL characters that belong to that class (not just the extras).
// Non-ASCII bytes are handled by the Unicode fold fallback in each classifier.
// The "alt N" annotations map to the alternatives in obfuscateQueryStringDefault.
const (
	classAlpha    uint8 = 1 << 0 // [a-zA-Z]              — PEM label chars (alt 6); base for derived classes
	classDigit    uint8 = 1 << 1 // [0-9]                 — base for derived classes
	classWord     uint8 = 1 << 2 // [a-zA-Z0-9_]          — \w as used in JWT segment/signature (alt 5)
	classBearer   uint8 = 1 << 3 // [a-zA-Z0-9._-]        — bearer token body (alt 2)
	classSSHBody  uint8 = 1 << 4 // [a-zA-Z0-9/+.]        — SSH RSA key body (alt 7)
	classJWTSeg   uint8 = 1 << 5 // [a-zA-Z0-9_=-] ≡ [\w=-]       — JWT header/payload segment char (alt 5)
	classJWTSig   uint8 = 1 << 6 // [a-zA-Z0-9_.+/=-] ≡ [\w.+\/=-] — JWT signature char (alt 5)
	classAlphaNum uint8 = 1 << 7 // [a-zA-Z0-9]           — short-token and GitHub token body (alts 3, 4)
)

// Regex-quantifier mirror constants. Each matches a fixed-length run in the
// original defaultQueryStringRegexp; keep these in sync if the regex changes.
const (
	shortTokenBodyLen = 13  // token(?::|%3A)[a-z0-9]{13}
	gitHubTokenLen    = 36  // gh[opsu]_[0-9a-zA-Z]{36}
	pemHyphenRun      = 5   // [\-]{5}
	sshRSAMinBody     = 100 // (?:[a-z0-9\/\.+]|%2F|%5C|%2B){100,}
)

// sensitiveBySecondByte sub-dispatches the first-byte sensitive bucket on the
// second byte. Both indices are ASCII lowercase letter offsets ('a'->0). Cells
// inherit the source list's order so prefix disambiguation ("pass_phrase"
// before "pass") is preserved. Sensitive keywords whose first byte is not
// a/c/p/s/t leave their row empty.
var sensitiveBySecondByte = func() [26][26][]string {
	var t [26][26][]string
	for f, kws := range sensitiveByFirstLetter {
		for _, kw := range kws {
			s := kw[1] - 'a'
			t[f][s] = append(t[f][s], kw)
		}
	}
	return t
}()

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
//
// This is a hand-written state machine. Each of the seven matcher branches
// implements one top-level alternative of defaultQueryStringRegexp, in the same
// order. The labels "alt 1 … alt 7" are used consistently across this file.
//
// Alt 1 — sensitive key + value (matcherSensitive → matchSensitiveKey):
//
//	(?i)(?:p(?:ass)?w(?:or)?d|pass(?:_?phrase)?|secret|
//	    (?:api_?|private_?|public_?|access_?|secret_?)key(?:_?id)?|token|
//	    consumer_?(?:id|key|secret)|sign(?:ed|ature)?|auth(?:entication|orization)?)
//	(?:(?:\s|%20)*(?:=|%3D)[^&]+                                      ← key=value
//	  |(?:"|%22)(?:\s|%20)*(?::|%3A)(?:\s|%20)*(?:"|%22)              ← JSON "key":"value"
//	   (?:%2[^2]|%[^2]|[^"%])+(?:"|%22))
//
// Alt 2 — bearer token (matcherBearer → matchBearerToken):
//
//	bearer(?:\s|%20)+[a-z0-9\._\-]
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
//	ey[I-L](?:[\w=-]|%3D)+\.ey[I-L](?:[\w=-]|%3D)+
//	(?:\.(?:[\w.+\/=-]|%3D|%2F|%2B)+)?
//
// Alt 6 — PEM private key (matcherPEM → matchPEMPrivateKey):
//
//	[\-]{5}BEGIN(?:[a-z\s]|%20)+PRIVATE(?:\s|%20)KEY[\-]{5}[^\-]+
//	[\-]{5}END(?:[a-z\s]|%20)+PRIVATE(?:\s|%20)KEY
//
// Alt 7 — SSH RSA key (matcherSSHRSA → matchSSHRSAKey):
//
//	ssh-rsa(?:\s|%20)*(?:[a-z0-9\/\.+]|%2F|%5C|%2B){100,}
//
// Note: "token" appears in both alt 1 (token=value) and alt 3 (token:…), so
// matcherStart['t'] sets both matcherSensitive and matcherShortToken bits.
func obfuscateQueryStringDefault(s string) string {
	var b strings.Builder
	last := 0
	// jwtSkipEnd is a watermark: matcherJWT is suppressed for positions in
	// [jwtSkipEnd_prev, jwtSkipEnd).  When matchJWT consumes a header+segment
	// but finds no '.' separator, it returns the end of that segment as segEnd.
	// Any 'e' within the consumed range will fail identically (same segment
	// chars, same absent '.'), so re-trying is pure quadratic waste.
	jwtSkipEnd := 0
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
		if mask&matcherJWT != 0 && pos >= jwtSkipEnd {
			if n, ok, segEnd := matchJWT(s, pos); ok {
				last = emitObfuscated(&b, s, last, pos, n)
				pos = last
				jwtSkipEnd = 0
				continue
			} else if segEnd > jwtSkipEnd {
				jwtSkipEnd = segEnd
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

// matchSensitiveKey implements alt 1 of defaultQueryStringRegexp: a sensitive
// keyword drawn from sensitiveByFirstLetter followed by either a key=value
// suffix (matchSensitiveKeyValue) or a JSON "key":"value" suffix
// (matchSensitiveKeyJSON). The keyword list is the expanded form of:
//
//	(?i)(?:p(?:ass)?w(?:or)?d|pass(?:_?phrase)?|secret|
//	    (?:api_?|private_?|public_?|access_?|secret_?)key(?:_?id)?|token|
//	    consumer_?(?:id|key|secret)|sign(?:ed|ature)?|auth(?:entication|orization)?)
func matchSensitiveKey(s string, pos int) (int, bool) {
	if pos >= len(s) {
		return 0, false
	}
	c := s[pos]
	if c >= utf8.RuneSelf {
		// Non-ASCII: can fold to any keyword-initial letter; scan all buckets.
		// Cold path for RFC-3986 query strings.
		for _, bucket := range sensitiveByFirstLetter {
			for _, keyword := range bucket {
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
	lc := toLowerASCII(c)
	if lc < 'a' || lc > 'z' {
		return 0, false
	}
	// Second-byte sub-dispatch when the second byte is also an ASCII letter:
	// only iterate keywords whose 2nd char folds to the input's 2nd char.
	// All sensitive keywords have an ASCII letter at position 1, so a
	// non-letter ASCII second byte can never match.
	var keywords []string
	if pos+1 < len(s) {
		c2 := s[pos+1]
		if c2 < utf8.RuneSelf {
			lc2 := toLowerASCII(c2)
			if 'a' <= lc2 && lc2 <= 'z' {
				keywords = sensitiveBySecondByte[lc-'a'][lc2-'a']
			}
		} else {
			// Non-ASCII second byte may fold to any letter; fall back
			// to the full first-letter bucket and let matchFoldLiteral
			// do the folding.
			keywords = sensitiveByFirstLetter[lc-'a']
		}
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

// matchBearerToken implements alt 2: bearer(?:\s|%20)+[a-z0-9\._\-]
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
// ey[I-L](?:[\w=-]|%3D)+\.ey[I-L](?:[\w=-]|%3D)+(?:\.(?:[\w.+\/=-]|%3D|%2F|%2B)+)?
// Header and payload segments each begin with "ey" followed by one of [I-L]
// (the base64 encoding of the JSON byte '{'); the optional third segment is the
// signature.
//
// matchJWT returns (matchedLen, ok, segEnd).
// segEnd is non-zero only on failure: it reports the end of the first
// header+segment scan when that scan succeeded but no '.' followed.  The
// outer loop uses segEnd to suppress redundant JWT re-anchors — any 'e' inside
// [pos, segEnd) would produce the same failing scan.
func matchJWT(s string, pos int) (n int, ok bool, segEnd int) {
	start := pos
	var matched bool
	if pos, matched = consumeJWTHeader(s, pos); !matched {
		return 0, false, 0
	}
	afterSeg, matched := consumeJWTSegment(s, pos)
	if !matched {
		return 0, false, 0
	}
	if afterSeg >= len(s) || s[afterSeg] != '.' {
		// Consumed header+segment but no dot: report segment end so the caller
		// can skip re-anchoring within this already-scanned range.
		return 0, false, afterSeg
	}
	pos = afterSeg + 1 // skip '.'
	if pos, matched = consumeJWTHeader(s, pos); !matched {
		return 0, false, 0
	}
	if pos, matched = consumeJWTSegment(s, pos); !matched {
		return 0, false, 0
	}
	if pos < len(s) && s[pos] == '.' {
		if end, matched := consumeJWTSignature(s, pos+1); matched {
			pos = end
		}
	}
	return pos - start, true, 0
}

// matchPEMPrivateKey implements alt 6:
// [\-]{5}BEGIN(?:[a-z\s]|%20)+PRIVATE(?:\s|%20)KEY[\-]{5}[^\-]+
// [\-]{5}END(?:[a-z\s]|%20)+PRIVATE(?:\s|%20)KEY
// The "PRIVATE KEY" substring may appear anywhere inside the PEM label (e.g.
// "ENCRYPTED PRIVATE KEY"), so the scanner advances through label chars and
// records the last position where "PRIVATE KEY" matches — implementing the
// greedy semantics of the original regexp.
func matchPEMPrivateKey(s string, pos int) (int, bool) {
	start := pos
	var ok bool
	if pos, ok = matchHyphens(s, pos, pemHyphenRun); !ok {
		return 0, false
	}
	if pos, ok = matchFoldLiteral(s, pos, "BEGIN"); !ok {
		return 0, false
	}
	// Scan the label for "PRIVATE KEY" and attempt body+end from the first
	// matching position.  Real PEM headers contain exactly one "PRIVATE KEY"
	// occurrence; trying every label position with a full body scan is O(N²)
	// for adversarial labels with repeated occurrences.
	labelPos, ok := consumePEMLabelChar(s, pos)
	if !ok {
		return 0, false
	}
	for {
		if afterKey, ok := matchPEMPrivateKeyLiteral(s, labelPos); ok {
			if end, ok := matchPEMBodyAndEnd(s, afterKey); ok {
				return end - start, true
			}
			// "PRIVATE KEY" found but body+end failed; no longer label suffix
			// can produce a valid PEM block from this BEGIN anchor.
			break
		}
		next, ok := consumePEMLabelChar(s, labelPos)
		if !ok {
			break
		}
		labelPos = next
	}
	return 0, false
}

// matchPEMBodyAndEnd implements [\-]{5}[^\-]+[\-]{5}END — PEM body and footer opener (alt 6).
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

// matchPEMFinalPrivateKey implements (?:[a-z\s]|%20)+PRIVATE(?:\s|%20)KEY in the PEM footer (alt 6).
func matchPEMFinalPrivateKey(s string, pos int) (int, bool) {
	labelPos, ok := consumePEMLabelChar(s, pos)
	if !ok {
		return 0, false
	}
	for {
		if end, ok := matchPEMPrivateKeyLiteral(s, labelPos); ok {
			return end, true
		}
		next, ok := consumePEMLabelChar(s, labelPos)
		if !ok {
			break
		}
		labelPos = next
	}
	return 0, false
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

// matchSSHRSAKey implements alt 7: ssh-rsa(?:\s|%20)*(?:[a-z0-9\/\.+]|%2F|%5C|%2B){100,}
//
// matchSSHRSAKey returns (matchedLen, ok, safeSkip).
// On failure (ok=false), safeSkip is the number of bytes from pos that the
// outer loop can safely skip without missing any other match. This avoids
// re-scanning the entire key body when the key is too short.
//
// Safe-skip correctness table (matchers vs. SSH-RSA body charset [a-zA-Z0-9/+.]):
//
//	sensitive key (p/a/s/c/t)  — letters present, but suffix needs '='/'%3D'/'"'/':', none in body → safe
//	bearer                     — needs space after "bearer"; space not in body → safe
//	short-token                — needs ':' after "token"; ':' not in body → safe
//	github                     — needs '_' after gh[opsu]; '_' not in body → safe
//	JWT (eyJ…)                 — '.' in body charset; 'e'/'E' can start JWT → NOT safe; stop skip at e/E
//	PEM (-----)                — '-' not in body → safe
//	SSH-RSA itself             — '-' not in body → cannot re-anchor → safe
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
	for pos < len(s) && s[pos] != '&' {
		pos++
	}
	return pos, true
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

// matchQuote implements (?:"|%22) — a literal or percent-encoded double-quote (alt 1 JSON suffix).
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

// matchHyphens implements [\-]{n} — exactly n consecutive hyphens (PEM fence, alt 6).
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

// consumeJWTHeader implements ey[I-L] — the fixed 3-byte prefix of each JWT segment (alt 5).
// [I-L] case-folds to [i-l]; the set {"i","j","k","l"} covers the base64
// encodings of the four possible first bytes of a JSON object: 0x7B = '{'.
func consumeJWTHeader(s string, pos int) (int, bool) {
	var ok bool
	if pos, ok = matchFoldLiteral(s, pos, "ey"); !ok {
		return 0, false
	}
	return consumeFoldedASCIISet(s, pos, "ijkl")
}

// consumeJWTSegment implements (?:[\w=-]|%3D)+ — base64url body of a JWT header or payload (alt 5).
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
