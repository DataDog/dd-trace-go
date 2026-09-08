// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"os"
	"path/filepath"
	"strings"
)

// validateLocalReplacements checks every module the tagger can discover
// before it is allowed to run. A local replacement must stay inside the
// pinned checkout both lexically and after symlink resolution, and its target
// must be a module root declaring the replaced module path.
func validateLocalReplacements(checkoutRoot string, excludedDirs []string) error {
	root, err := filepath.Abs(checkoutRoot)
	if err != nil {
		return typedGenerationError("replacement_root_invalid")
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return typedGenerationError("replacement_root_invalid")
	}
	// Keep this fixed set aligned with the trusted tagger's built-in excluded
	// directories. Caller-supplied exclusions are additive in the tagger.
	allExcluded := append([]string{"_tools", ".claude", ".github", "tools"}, excludedDirs...)
	excluded := make(map[string]bool, len(allExcluded))
	for _, value := range allExcluded {
		path := value
		if !filepath.IsAbs(path) {
			path = filepath.Join(root, path)
		}
		path, err = filepath.Abs(path)
		if err != nil || !pathWithin(root, path) {
			return typedGenerationError("replacement_excluded_path_invalid")
		}
		excluded[filepath.Clean(path)] = true
	}
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return typedGenerationError("replacement_walk_failed")
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || excluded[filepath.Clean(path)] {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Name() != "go.mod" {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return typedGenerationError("replacement_modfile_symlink")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return typedGenerationError("replacement_modfile_read_failed")
		}
		mod, err := ParseModFile(data)
		if err != nil {
			return typedGenerationError("replacement_modfile_invalid")
		}
		moduleDir := filepath.Dir(path)
		for _, replacement := range mod.Replace {
			if err := validateOneLocalReplacement(root, resolvedRoot, moduleDir, replacement); err != nil {
				return err
			}
		}
		return nil
	})
}

func validateOneLocalReplacement(root, resolvedRoot, moduleDir string, replacement ModFileReplace) error {
	pathLike := replacement.IsFilesystemReplacement() || filepath.IsAbs(replacement.NewPath) ||
		strings.HasPrefix(replacement.NewPath, "./") || strings.HasPrefix(replacement.NewPath, "../") || hasWindowsVolume(replacement.NewPath)
	if !pathLike {
		return nil
	}
	if replacement.NewVersion != "" {
		return typedGenerationError("versioned_local_replacement")
	}
	if filepath.IsAbs(replacement.NewPath) || hasWindowsVolume(replacement.NewPath) {
		return typedGenerationError("absolute_local_replacement")
	}
	target := filepath.Clean(filepath.Join(moduleDir, replacement.NewPath))
	if !pathWithin(root, target) {
		return typedGenerationError("local_replacement_escape")
	}
	info, err := os.Stat(target)
	if err != nil || !info.IsDir() {
		return typedGenerationError("local_replacement_missing")
	}
	resolvedTarget, err := filepath.EvalSymlinks(target)
	if err != nil || !pathWithin(resolvedRoot, resolvedTarget) {
		return typedGenerationError("local_replacement_symlink_escape")
	}
	targetModData, err := os.ReadFile(filepath.Join(resolvedTarget, "go.mod"))
	if err != nil {
		return typedGenerationError("local_replacement_not_module_root")
	}
	targetMod, err := ParseModFile(targetModData)
	if err != nil {
		return typedGenerationError("local_replacement_not_module_root")
	}
	if targetMod.ModulePath != replacement.OldPath {
		return typedGenerationError("local_replacement_module_mismatch")
	}
	return nil
}

func pathWithin(root, candidate string) bool {
	rel, err := filepath.Rel(root, candidate)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
