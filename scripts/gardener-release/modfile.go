// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"strings"
)

// ModFileRequire is one `require` directive: a module path pinned to a
// version, with its direct/indirect classification.
type ModFileRequire struct {
	Path     string
	Version  string
	Indirect bool
}

// ModFileReplace is one `replace` directive. NewPath is a local filesystem
// path when NewVersion is empty; otherwise it is a module path pinned to
// NewVersion. Go's grammar distinguishes the two by whether the
// replacement target looks like a filesystem path (starts with "./" or
// "../", or is an absolute path) or carries an explicit "=> path version"
// version.
type ModFileReplace struct {
	OldPath    string
	OldVersion string
	NewPath    string
	NewVersion string
}

// ModFile is the minimal subset of go.mod's grammar this package enforces:
// the module path, the go/toolchain directives (kept as raw strings so an
// unexpected format cannot be silently normalized away), and every
// require/replace directive. It intentionally does not model `exclude` or
// `retract` bodies beyond preserving their raw lines for byte-comparison
// in validate.go; this parser exists only to enforce B07's five
// unpublished-dependency resolution rules, not to reimplement Go module
// resolution semantics.
type ModFile struct {
	ModulePath string
	Go         string
	Toolchain  string
	Godebug    []string
	Require    []ModFileRequire
	Replace    []ModFileReplace
}

// ParseModFile parses a minimal, well-defined subset of go.mod syntax
// sufficient to extract the module path and require/replace directives.
// It rejects anything it cannot confidently parse (block comments, escaped
// strings, directives it does not recognize inside a block) rather than
// silently ignoring lines: a validator that silently drops an
// unrecognized replace directive could approve a source tree that lacks
// the very control it is supposed to enforce.
func ParseModFile(data []byte) (ModFile, error) {
	text := string(data)
	if strings.Contains(text, "/*") {
		return ModFile{}, typedContractError("unsupported_modfile_syntax")
	}
	mod := ModFile{}
	lines := strings.Split(text, "\n")
	var blockKind string
	sawModule := false
	for i := 0; i < len(lines); i++ {
		rawTrimmed := strings.TrimSpace(lines[i])
		if rawTrimmed == "" {
			continue
		}
		if blockKind != "" {
			if rawTrimmed == ")" {
				blockKind = ""
				continue
			}
			// require/replace lines keep their trailing "// indirect"
			// comment intact: parseRequireLine inspects it directly.
			// Every other block kind is comment-stripped like the rest of
			// the file, since exclude/retract bodies are only ever
			// compared byte-for-byte in validate.go, never re-serialized.
			line := rawTrimmed
			if blockKind != "require" && blockKind != "replace" {
				line = strings.TrimSpace(stripLineComment(rawTrimmed))
				if line == "" {
					continue
				}
			}
			if err := parseModFileBodyLine(&mod, blockKind, line); err != nil {
				return ModFile{}, err
			}
			continue
		}
		trimmed := strings.TrimSpace(stripLineComment(rawTrimmed))
		if trimmed == "" {
			continue
		}
		fields := strings.Fields(trimmed)
		keyword := fields[0]
		switch keyword {
		case "module":
			if sawModule {
				return ModFile{}, typedContractError("duplicate_modfile_module")
			}
			rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "module"))
			path, err := unquoteModFileToken(rest)
			if err != nil {
				return ModFile{}, err
			}
			mod.ModulePath = path
			sawModule = true
		case "go":
			mod.Go = strings.TrimSpace(strings.TrimPrefix(trimmed, "go"))
		case "toolchain":
			mod.Toolchain = strings.TrimSpace(strings.TrimPrefix(trimmed, "toolchain"))
		case "godebug":
			rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "godebug"))
			if rest == "(" {
				blockKind = "godebug"
				continue
			}
			mod.Godebug = append(mod.Godebug, rest)
		case "require", "replace", "exclude", "retract":
			rest := strings.TrimSpace(strings.TrimPrefix(trimmed, keyword))
			if rest == "(" {
				blockKind = keyword
				continue
			}
			// Single-line form: re-derive from rawTrimmed (not the
			// comment-stripped trimmed) so a single-line "require x v1 //
			// indirect" still carries its indirect marker.
			rawRest := strings.TrimSpace(strings.TrimPrefix(rawTrimmed, keyword))
			if keyword == "require" || keyword == "replace" {
				rest = rawRest
			}
			if err := parseModFileBodyLine(&mod, keyword, rest); err != nil {
				return ModFile{}, err
			}
		default:
			return ModFile{}, typedContractError("unsupported_modfile_syntax")
		}
	}
	if blockKind != "" {
		return ModFile{}, typedContractError("unsupported_modfile_syntax")
	}
	if !sawModule || mod.ModulePath == "" {
		return ModFile{}, typedContractError("missing_modfile_module")
	}
	return mod, nil
}

func parseModFileBodyLine(mod *ModFile, kind, line string) error {
	switch kind {
	case "require":
		req, err := parseRequireLine(line)
		if err != nil {
			return err
		}
		mod.Require = append(mod.Require, req)
		return nil
	case "replace":
		rep, err := parseReplaceLine(line)
		if err != nil {
			return err
		}
		mod.Replace = append(mod.Replace, rep)
		return nil
	case "exclude", "retract":
		// Recognized but not modeled: validate.go compares go.mod files by
		// exact byte content for everything except manifest-approved
		// require/replace changes, so exclude/retract bodies are covered
		// by that byte comparison rather than by a dedicated parsed type.
		return nil
	case "godebug":
		mod.Godebug = append(mod.Godebug, line)
		return nil
	default:
		return typedContractError("unsupported_modfile_syntax")
	}
}

func parseRequireLine(line string) (ModFileRequire, error) {
	indirect := false
	if idx := strings.Index(line, "//"); idx >= 0 {
		if strings.TrimSpace(line[idx+2:]) == "indirect" {
			indirect = true
		}
		line = strings.TrimSpace(line[:idx])
	}
	fields := strings.Fields(line)
	if len(fields) != 2 {
		return ModFileRequire{}, typedContractError("unsupported_modfile_syntax")
	}
	path, err := unquoteModFileToken(fields[0])
	if err != nil {
		return ModFileRequire{}, err
	}
	return ModFileRequire{Path: path, Version: fields[1], Indirect: indirect}, nil
}

func parseReplaceLine(line string) (ModFileReplace, error) {
	arrow := strings.Index(line, "=>")
	if arrow < 0 {
		return ModFileReplace{}, typedContractError("unsupported_modfile_syntax")
	}
	left := strings.Fields(strings.TrimSpace(line[:arrow]))
	right := strings.Fields(strings.TrimSpace(line[arrow+2:]))
	if len(left) < 1 || len(left) > 2 || len(right) < 1 || len(right) > 2 {
		return ModFileReplace{}, typedContractError("unsupported_modfile_syntax")
	}
	oldPath, err := unquoteModFileToken(left[0])
	if err != nil {
		return ModFileReplace{}, err
	}
	oldVersion := ""
	if len(left) == 2 {
		oldVersion = left[1]
	}
	newPath, err := unquoteModFileToken(right[0])
	if err != nil {
		return ModFileReplace{}, err
	}
	newVersion := ""
	if len(right) == 2 {
		newVersion = right[1]
	}
	return ModFileReplace{OldPath: oldPath, OldVersion: oldVersion, NewPath: newPath, NewVersion: newVersion}, nil
}

// IsFilesystemReplacement reports whether a replace directive's target is
// a local filesystem path rather than a module path pinned to a version,
// using Go's own rule: a filesystem replacement has no version and its
// path starts with "./", "../", or is absolute (for either OS). A
// version-qualified target is always a module path, never a filesystem
// path, even if it happens to contain a slash.
func (r ModFileReplace) IsFilesystemReplacement() bool {
	if r.NewVersion != "" {
		return false
	}
	return strings.HasPrefix(r.NewPath, "./") || strings.HasPrefix(r.NewPath, "../") ||
		strings.HasPrefix(r.NewPath, "/") || hasWindowsVolume(r.NewPath)
}

func hasWindowsVolume(path string) bool {
	return len(path) >= 2 && path[1] == ':' && ((path[0] >= 'a' && path[0] <= 'z') || (path[0] >= 'A' && path[0] <= 'Z'))
}

func stripLineComment(line string) string {
	if idx := strings.Index(line, "//"); idx >= 0 {
		return line[:idx]
	}
	return line
}

// unquoteModFileToken removes a go.mod token's surrounding quotes if
// present. go.mod accepts both bare unquoted tokens (the common case for
// module/require paths) and double-quoted or backtick-quoted strings; this
// helper rejects escape sequences rather than interpreting them, since no
// legitimate module path or version in this repository's go.mod files
// requires one, and silently interpreting an escape could let a crafted
// value smuggle a path separator past later validation.
func unquoteModFileToken(token string) (string, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return "", typedContractError("unsupported_modfile_syntax")
	}
	if strings.HasPrefix(token, "`") {
		if !strings.HasSuffix(token, "`") || len(token) < 2 {
			return "", typedContractError("unsupported_modfile_syntax")
		}
		return token[1 : len(token)-1], nil
	}
	if strings.HasPrefix(token, `"`) {
		if !strings.HasSuffix(token, `"`) || len(token) < 2 {
			return "", typedContractError("unsupported_modfile_syntax")
		}
		inner := token[1 : len(token)-1]
		if strings.Contains(inner, `\`) {
			return "", typedContractError("unsupported_modfile_syntax")
		}
		return inner, nil
	}
	if strings.ContainsAny(token, " \t\"'`") {
		return "", typedContractError("unsupported_modfile_syntax")
	}
	return token, nil
}

// FindReplace returns the replace directive for modulePath, if any, and
// whether one was found. go.mod allows at most one replace per module
// path; ParseModFile does not itself reject a duplicate (mirroring `go
// mod tidy`'s own error rather than guessing which one wins), so callers
// that care must check for duplicates explicitly.
func (m ModFile) FindReplace(modulePath string) (ModFileReplace, bool) {
	var found ModFileReplace
	seen := false
	last := -1
	for i, rep := range m.Replace {
		if rep.OldPath == modulePath {
			last = i
		}
	}
	if last >= 0 {
		found = m.Replace[last]
		seen = true
	}
	return found, seen
}

// DuplicateReplacePaths returns every module path that has more than one
// replace directive, so callers can reject ambiguous go.mod files instead
// of silently picking the last one.
func (m ModFile) DuplicateReplacePaths() []string {
	counts := map[string]int{}
	for _, rep := range m.Replace {
		counts[rep.OldPath]++
	}
	var duplicates []string
	for path, count := range counts {
		if count > 1 {
			duplicates = append(duplicates, path)
		}
	}
	return duplicates
}
