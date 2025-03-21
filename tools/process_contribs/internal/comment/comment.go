package comment

import (
	"fmt"
	"regexp"
)

var entrypointCommentRegex = regexp.MustCompile(`^(?:\/\/)?\s*ddtrace:entrypoint:(\S+)(?:\s+(.*))?$`)

type Comment struct {
	Command   string
	Arguments map[string]string
}

func (c Comment) String() string {
	return fmt.Sprintf("command: %s | arguments: %v", c.Command, c.Arguments)
}

func ParseComment(s string) (Comment, bool) {
	// Match the command
	match := entrypointCommentRegex.FindStringSubmatch(s)
	if match == nil {
		return Comment{}, false
	}

	// Extract command and named arguments
	command := match[1]
	namedArgs := parseNamedArgs(match[2])
	namedArgs["__raw_args"] = match[2]

	entry := Comment{
		Command:   command,
		Arguments: namedArgs,
	}
	return entry, true
}

func parseNamedArgs(raw string) map[string]string {
	namedArgs := make(map[string]string)

	var key, value string
	inQuotes := false
	readingValue := false

	for i := 0; i < len(raw); i++ {
		char := raw[i]

		switch {
		case char == '"' && readingValue:
			// Toggle quote state when inside a value
			inQuotes = !inQuotes

		case char == ':' && !readingValue:
			// Found key-value separator
			readingValue = true

		case char == ' ' && !inQuotes:
			// End of a key-value pair (only if not inside quotes)
			if key != "" && value != "" {
				namedArgs[key] = value
			}
			key, value = "", ""
			readingValue = false

		default:
			// Append to key or value
			if readingValue {
				value += string(char)
			} else {
				key += string(char)
			}
		}
	}

	// Store last key-value pair
	if key != "" && value != "" {
		namedArgs[key] = value
	}

	// Remove surrounding quotes from values
	for k, v := range namedArgs {
		if len(v) > 1 && v[0] == '"' && v[len(v)-1] == '"' {
			namedArgs[k] = v[1 : len(v)-1]
		}
	}

	return namedArgs
}
