// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package internal

import (
	"strconv"
	"strings"

	"github.com/DataDog/dd-trace-go/v2/internal/env"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

// BoolEnv returns the parsed boolean value of an environment variable, or
// def otherwise.
func BoolEnv(key string, def bool) bool {
	vv, ok := BoolEnvNoDefault(key)
	if !ok {
		return def
	}
	return vv
}

// BoolEnvNoDefault returns the parsed boolean value of an environment variable. The second returned bool signals if
// the value was set and was a correct boolean value.
func BoolEnvNoDefault(key string) (bool, bool) {
	vv, ok := env.Lookup(key)
	if !ok {
		return false, false
	}
	v, err := strconv.ParseBool(vv)
	if err != nil {
		log.Warn("Non-boolean value for env var %s. Parse failed with error: %v", key, err.Error())
		return false, false
	}
	return v, true
}

// ForEachStringTag runs fn on every key val pair encountered in str.
// str may contain multiple key val pairs separated by either space
// or comma (but not a mixture of both), and each key val pair is separated by a delimiter.
func ForEachStringTag(str string, delimiter string, fn func(key string, val string)) {
	sep := " "
	if strings.Index(str, ",") > -1 {
		// falling back to comma as separator
		sep = ","
	}
	for tag := range strings.SplitSeq(str, sep) {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		kv := strings.SplitN(tag, delimiter, 2)
		key := strings.TrimSpace(kv[0])
		if key == "" {
			continue
		}
		var val string
		if len(kv) == 2 {
			val = strings.TrimSpace(kv[1])
		}
		fn(key, val)
	}
}

// ParseTagString returns tags parsed from string as map
func ParseTagString(str string) map[string]string {
	res := make(map[string]string)
	ForEachStringTag(str, DDTagsDelimiter, func(key, val string) { res[key] = val })
	return res
}

// BoolVal returns the parsed boolean value of string val, or def if not parseable
func BoolVal(val string, def bool) bool {
	v, err := strconv.ParseBool(val)
	if err != nil {
		return def
	}
	return v
}
