// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package rum

import (
	"net/http"
	"unicode"
)

type state int

const (
	sInit  state = iota // looking for '<'
	sLt                 // saw '<', expect '/'
	sSlash              // saw "</", allow spaces, expect 'h'
	sH                  // expect 'e' (no spaces allowed)
	sE                  // expect 'a' (no spaces allowed)
	sA                  // expect 'd' (no spaces allowed)
	sD                  // saw "...head", allow spaces, expect '>'
	sDone               // "</head>" found
)

var (
	snippet = []byte("<snippet>")
)

// injector of a POC for RUM snippet injection.
// It doesn't handle Content-Length manipulation.
// It isn't concurrent safe.
type injector struct {
	wrapped       http.ResponseWriter
	st            state
	lastSeen      int
	seenInCurrent bool
	buf           [][]byte
}

// Header implements http.ResponseWriter.
func (ij *injector) Header() http.Header {
	// TODO: this is a good place to inject Content-Length to the right
	// length, not the original one, if injection happened.
	return ij.wrapped.Header()
}

// WriteHeader implements http.ResponseWriter.
func (ij *injector) WriteHeader(statusCode int) {
	ij.wrapped.WriteHeader(statusCode)
}

// Write implements http.ResponseWriter.
// There are no guarantees that Write will be called with the whole payload.
// We need to keep state of what we've written so far to find the pattern
// "</head>" in all its variants.
func (ij *injector) Write(chunk []byte) (int, error) {
	prev := ij.st
	// If we've already found the pattern, just write the chunk.
	if prev == sDone {
		return ij.wrapped.Write(chunk)
	}
	ij.match(chunk)
	if prev == sInit {
		// No partial or full match done so far.
		if ij.st == sInit {
			return ij.wrapped.Write(chunk)
		}
		// Full match done in the chunk.
		if ij.st == sDone {
			ij.st = sDone
			sz, err := multiWrite(ij.wrapped, chunk[:ij.lastSeen], snippet, chunk[ij.lastSeen:])
			if err != nil {
				return sz, err
			}
			return sz, nil
		}
		// Partial match in progress. We buffer the write.
		// ij.lastSeen should be the index of the first byte of the match
		// of the first chunk.
		ij.buf = append(ij.buf, chunk)
		return 0, nil
	}
	if ij.st != sDone {
		// Partial match in progress. We buffer the write.
		ij.buf = append(ij.buf, chunk)
		return 0, nil
	}
	// Partial match done.
	var (
		total int
		sz    int
		err   error
	)
	ij.buf = append(ij.buf, chunk)
	seenAt := 0
	if ij.seenInCurrent {
		seenAt = len(ij.buf) - 1
	}
	// Write the chunks before the chunk where the pattern starts.
	sz, err = multiWrite(ij.wrapped, ij.buf[:seenAt]...)
	if err != nil {
		return sz, err
	}
	total += sz
	// Write the snippet in the chunk where the pattern starts.
	head := ij.buf[seenAt]
	sz, err = multiWrite(ij.wrapped, head[:ij.lastSeen], snippet, head[ij.lastSeen:])
	if err != nil {
		return sz, err
	}
	total += sz
	// Write the rest of the buffered chunks.
	sz, err = multiWrite(ij.wrapped, ij.buf[seenAt+1:]...)
	if err != nil {
		return sz, err
	}
	total += sz
	// Reset the buffer.
	ij.buf = ij.buf[:0]
	return total, nil
}

func multiWrite(w http.ResponseWriter, chunks ...[]byte) (int, error) {
	if len(chunks) == 0 {
		return 0, nil
	}
	sz := 0
	for _, chunk := range chunks {
		n, err := w.Write(chunk)
		if err != nil {
			return sz, err
		}
		sz += n
	}
	return sz, nil
}

// match updates the state of the injector according on what step of
// the pattern "</head>" have been found.
func (ij *injector) match(p []byte) {
	if ij.st == sDone {
		return
	}
	ij.seenInCurrent = false
	for i := 0; i < len(p); i++ {
		c := unicode.ToLower(rune(p[i]))
		switch ij.st {
		case sInit:
			ij.transition('<', c, sLt, i)
		case sLt: // expect '/'
			ij.transition('/', c, sSlash, i)
		case sSlash: // expect 'h'
			if unicode.IsSpace(c) {
				continue
			}
			ij.transition('h', c, sH, i)
		case sH: // expect 'e'
			ij.transition('e', c, sE, i)
		case sE: // expect 'a'
			ij.transition('a', c, sA, i)
		case sA: // expect 'd'
			ij.transition('d', c, sD, i)
		case sD: // expect '>'
			if unicode.IsSpace(c) {
				continue
			}
			ij.transition('>', c, sDone, i)
		}
	}
}

func (ij *injector) transition(expected, current rune, target state, pos int) {
	switch current {
	case expected:
		ij.st = target
	case '<':
		ij.st = sLt
	default:
		ij.st = sInit
	}
	if current == '<' {
		ij.lastSeen = pos
		ij.seenInCurrent = true
	}
}

// Flush flushes the buffered chunks to the wrapped writer.
func (ij *injector) Flush() (int, error) {
	if len(ij.buf) == 0 {
		return 0, nil
	}
	sz, err := multiWrite(ij.wrapped, ij.buf...)
	ij.buf = ij.buf[:0]
	return sz, err
}

// Reset resets the state of the injector.
func (i *injector) Reset() {
	i.st = sInit
	i.lastSeen = -1
	i.buf = i.buf[:0]
}

func NewInjector(fn func(w http.ResponseWriter, r *http.Request)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ij := &injector{
			wrapped:  w,
			lastSeen: -1,
			buf:      make([][]byte, 0, 10),
		}
		fn(ij, r)
		ij.Flush()
	})
}
