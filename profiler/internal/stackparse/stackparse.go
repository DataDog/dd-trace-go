// Package stackparse is an experimental replacement for pkg panicparse. The
// goal for the parser is to be:
//
// 1. Safe: No panics should be thrown.
// 2. Simple: Keep this pkg small and easy to modify.
// 3. Forgiving: Favor producing partial results over no results, even if the
// input data is different than expected.
// 4. Efficient: Try to be at least 10x faster than panicparse as long as it
// doesn't complicate things.
package stackparse

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type parserState int

const (
	stateHeader = iota
	stateStackFunc
	stateStackFile
	stateCreatedBy
	stateCreatedByFunc
	stateCreatedByFile
)

// Parse parses a goroutines stack trace dump as produced by runtime.Stack().
// The parser is forgiving and will continue parsing even when encountering
// unexpected data. When this happens it will try to discard the entire
// goroutine that encountered the problem and return an error in addition to
// the goroutines it was able to parse successfully.
//
// The parser expects the input to roughly follow the ABNF grammar below:
//
// goroutines = goroutine *("\n" goroutine)
// goroutine  = header stack created_by
//
// header     = "goroutine " id "[" state wait "]\n"
// id         = 1*DIGIT
// state      = 1*OCTET ; excluding []
// wait       = *1(", " minutes " minutes")
// minutes    = 1*DIGIT
//
// stack      = 1*frame
// frame      = func file
// func       = func_name "(" args ")\n"
// func_name  = 1*OCTET ; excluding: ()
// args       = 1*OCTET ; excluding: ()
// file       = "\t" path ":" line offset "\n"
// path       = 1*OCTET
// line       = 1*DIGIT
// offset     = "+0x" 1*HEXDIG
func Parse(r io.Reader) ([]*Goroutine, *Errors) {
	var (
		sc      = bufio.NewScanner(r)
		state   parserState
		lineNum int
		line    string

		goroutines []*Goroutine
		g          *Goroutine
		f          *Frame

		errs           = &Errors{}
		abortGoroutine = func(msg string) {
			err := fmt.Errorf(
				"%s on line %d, but got: %s",
				msg,
				lineNum,
				line,
			)
			errs.Errors = append(errs.Errors, err)
			goroutines = goroutines[0 : len(goroutines)-1]
			state = stateHeader
		}
	)

	// This for loop implements a simple line based state machine for parsing
	// the goroutines dump.
	for sc.Scan() {
		line = sc.Text()
		lineNum++

	statemachine:
		switch state {
		case stateHeader:
			g = parseGoroutineHeader(line)
			if g == nil {
				continue
			}
			goroutines = append(goroutines, g)
			state = stateStackFunc
		case stateStackFunc, stateCreatedByFunc:
			f = parseFunc(line, state)
			if f == nil {
				abortGoroutine("expected function call")
				continue
			}
			if state == stateStackFunc {
				g.Stack = append(g.Stack, f)
				state = stateStackFile
			} else {
				g.CreatedBy = f
				state = stateCreatedByFile
			}
		case stateStackFile, stateCreatedByFile:
			if !parseFile(line, f) {
				abortGoroutine("expected file:line ref")
				continue
			}
			state = stateCreatedBy
		case stateCreatedBy:
			if strings.HasPrefix(line, createdByPrefix) {
				line = line[len(createdByPrefix):]
				state = stateCreatedByFunc
				goto statemachine
			}
			state = stateHeader
		}
	}

	if len(errs.Errors) > 0 {
		return goroutines, errs
	}
	return goroutines, nil
}

const (
	goroutinePrefix = "goroutine "
	createdByPrefix = "created by "
)

var goroutineHeader = regexp.MustCompile(
	"^" + goroutinePrefix + `(\d+) \[(.+?)(?:, (\d+) minutes)?\]:$`,
)

// parseGoroutineHeader parses a goroutine header line and returns a new
// Goroutine on success or nil on error.
//
// Example Input:
// goroutine 1 [chan receive, 6883 minutes]:
//
// Example Output:
// &Goroutine{ID: 1, State "chan receive", Waitduration: 6883*time.Minute}
func parseGoroutineHeader(line string) *Goroutine {
	// optimization: bailing early if this doesn't match is 2x faster than
	// running the regex right away.
	if !strings.HasPrefix(line, goroutinePrefix) {
		return nil
	}

	// TODO(fg) would probably be faster if we didn't use a regexp for this, but
	// might be more hassle than its worth.
	m := goroutineHeader.FindStringSubmatch(line)
	if len(m) != 4 {
		return nil
	}
	var (
		id          = m[1]
		state       = m[2]
		waitminutes = m[3]

		g   = &Goroutine{State: state}
		err error
	)

	// regex currently sucks `abc minutes` into the state string if abc is
	// non-numeric, let's not consider this a valid goroutine.
	if strings.HasSuffix(state, " minutes") {
		return nil
	} else if g.ID, err = strconv.Atoi(id); err != nil {
		// should be impossible to end up here
		return nil
	} else if waitminutes == "" {
		// do nothing, goroutine isn't waiting
	} else if min, err := strconv.Atoi(waitminutes); err != nil {
		// should be impossible to end up here
		return nil
	} else {
		g.Waitduration = time.Duration(min) * time.Minute
	}
	return g
}

// parseFunc parse a func call with potential argument addresses and returns a
// new Frame for it on success or nil on error.
//
// Example Input:
// runtime/pprof.writeGoroutineStacks(0x2b016e0, 0xc0995cafc0, 0xc00468e150, 0x0)
//
// Example Output:
// &Frame{Func: "runtime/pprof.writeGoroutineStacks"}
func parseFunc(line string, state parserState) *Frame {
	if state == stateCreatedByFunc {
		return &Frame{Func: line}
	}

	var (
		openIndex  int
		closeIndex int
	)
	for i, r := range line {
		switch r {
		case '(':
			openIndex = i
		case ')':
			closeIndex = i
		}
	}
	if openIndex >= closeIndex {
		return nil
	}
	return &Frame{Func: line[0:openIndex]}
}

// parseFile parses a file line and updates f accordingly or returns false on
// error.
//
// Example Input:
// /root/go1.15.6.linux.amd64/src/net/http/server.go:2969 +0x36c
//
// Example Update:
// &Frame{File: "/root/go1.15.6.linux.amd64/src/net/http/server.go", Line: 2969}
func parseFile(line string, f *Frame) bool {
	if len(line) == 0 || line[0] != '\t' {
		return false
	}

	line = line[1:]
	for i, c := range line {
		if c == ':' {
			if f.File != "" {
				return false
			}
			f.File = line[0:i]
		} else if c == ' ' || i+1 == len(line) {
			if f.File == "" {
				return false
			}
			var err error
			f.Line, err = strconv.Atoi(line[len(f.File)+1 : i])
			return err == nil
		}
	}
	return false
}

type Goroutine struct {
	ID           int
	State        string
	Waitduration time.Duration
	Stack        []*Frame
	CreatedBy    *Frame
}

type Frame struct {
	Func string
	Line int
	File string
}

type Errors struct {
	Errors []error
}

func (e *Errors) Error() string {
	return fmt.Sprintf("stackparse: %d errors occurred", len(e.Errors))
}
