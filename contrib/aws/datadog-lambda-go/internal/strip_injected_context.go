// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package internal

import (
	"bytes"
	"encoding/json"
)

var (
	_datadog    = []byte("\"_datadog\":")
	_datadogLen = len(_datadog)
)

func isWhiteSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}

func StripInjectedContext(msg json.RawMessage) json.RawMessage {
	start := bytes.Index(msg, _datadog)
	if start < 0 {
		return msg
	}
	var prefixLen, suffixLen, end int
	var openBraceFound, openQuoteFound, leadingCommaFound bool
	for loc := start - 1; loc >= 0; loc-- {
		if msg[loc] == ',' {
			leadingCommaFound = true
			prefixLen = start - loc
			break
		} else if !isWhiteSpace(msg[loc]) {
			// not whitespace
			break
		}
	}
	for loc := start + _datadogLen; loc < len(msg); loc++ {
		if msg[loc] == '"' && !openBraceFound {
			openQuoteFound = true
		}
		if msg[loc] == '{' {
			if openBraceFound {
				// expecting just one open brace
				break
			}
			openBraceFound = true
		}
		if msg[loc] == '}' {
			if openQuoteFound {
				if loc+1 < len(msg) && msg[loc+1] == '"' {
					end = loc + 2
					break
				} else {
					// expecting a closing quote after the closing brace
					break
				}
			}
			end = loc + 1
			break
		}
	}
	if end > start {
		if !leadingCommaFound {
			for loc := end; loc < len(msg); loc++ {
				if msg[loc] == ',' {
					suffixLen = loc - end + 1
					for loc2 := loc + 1; isWhiteSpace(msg[loc2]); loc2++ {
						suffixLen++
					}
					break
				} else if !isWhiteSpace(msg[loc]) {
					// not whitespace
					break
				}
			}
		}
		return append(msg[:start-prefixLen], msg[end+suffixLen:]...)
	}
	return msg
}
