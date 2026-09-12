// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"os"
	"path/filepath"
)

const (
	CanonicalGitHubRemote       = "https://github.com/DataDog/dd-trace-go.git"
	PublicationTokenEnvironment = "GARDENER_RELEASE_GITHUB_TOKEN"
)

// GitHubGitCredentials owns a non-secret fixed askpass helper and private
// HOME/XDG directories. The token is never written to disk; Git receives it
// only through one fixed environment variable in the protected subprocess.
type GitHubGitCredentials struct {
	directory string
	askpass   string
	home      string
	xdg       string
	token     string
}

func LoadProtectedGitHubGitCredentials() (*GitHubGitCredentials, error) {
	token, ok := os.LookupEnv(PublicationTokenEnvironment)
	if !ok || token == "" {
		return nil, newReleaseError(ErrorClassEvidenceIncomplete, "publication_token_unavailable")
	}
	directory, err := os.MkdirTemp("", "gardener-release-git-auth-")
	if err != nil {
		return nil, wrapReleaseError(ErrorClassEvidenceIncomplete, "git_auth_materialize_failed", err)
	}
	credentials := &GitHubGitCredentials{directory: directory, askpass: filepath.Join(directory, "askpass"), home: filepath.Join(directory, "home"), xdg: filepath.Join(directory, "xdg"), token: token}
	cleanup := true
	defer func() {
		if cleanup {
			_ = credentials.Close()
		}
	}()
	if err := os.Chmod(directory, 0700); err != nil {
		return nil, wrapReleaseError(ErrorClassEvidenceIncomplete, "git_auth_materialize_failed", err)
	}
	if err := os.Mkdir(credentials.home, 0700); err != nil {
		return nil, wrapReleaseError(ErrorClassEvidenceIncomplete, "git_auth_materialize_failed", err)
	}
	if err := os.Mkdir(credentials.xdg, 0700); err != nil {
		return nil, wrapReleaseError(ErrorClassEvidenceIncomplete, "git_auth_materialize_failed", err)
	}
	const helper = `#!/bin/sh
case "$1" in
  Username*) printf '%s\n' 'x-access-token' ;;
  Password*) test -n "${GARDENER_RELEASE_GITHUB_TOKEN:-}" || exit 1; printf '%s\n' "$GARDENER_RELEASE_GITHUB_TOKEN" ;;
  *) exit 2 ;;
esac
`
	if err := os.WriteFile(credentials.askpass, []byte(helper), 0700); err != nil {
		return nil, wrapReleaseError(ErrorClassEvidenceIncomplete, "git_auth_materialize_failed", err)
	}
	cleanup = false
	return credentials, nil
}

func ValidateCanonicalGitHubRemote(remote string) error {
	if remote != CanonicalGitHubRemote {
		return newReleaseError(ErrorClassContractMismatch, "invalid_production_remote")
	}
	return nil
}

// Command returns a credentialed Git command only for the fixed production
// remote. The environment is private to the subprocess and callers must never
// log it. No credential helper, terminal fallback, global config, or reusable
// redirect credential is enabled.
func (c *GitHubGitCredentials) Command(dir string, args ...string) (Command, error) {
	if c == nil || c.directory == "" || c.askpass == "" || c.token == "" {
		return Command{}, newReleaseError(ErrorClassEvidenceIncomplete, "publication_token_unavailable")
	}
	info, err := os.Stat(c.askpass)
	if err != nil || info.Mode().Perm() != 0700 || !filepath.IsAbs(c.askpass) {
		return Command{}, newReleaseError(ErrorClassEvidenceIncomplete, "git_auth_permissions_invalid")
	}
	remoteFound := false
	for _, arg := range args {
		if arg == CanonicalGitHubRemote {
			remoteFound = true
		}
		if len(arg) >= 8 && (arg[:7] == "http://" || arg[:8] == "https://") && arg != CanonicalGitHubRemote {
			return Command{}, newReleaseError(ErrorClassContractMismatch, "invalid_production_remote")
		}
	}
	if !remoteFound {
		return Command{}, newReleaseError(ErrorClassContractMismatch, "production_remote_required")
	}
	env := gitStateEnv()
	env = append(env,
		"GIT_ASKPASS="+c.askpass,
		"GIT_ASKPASS_REQUIRE=force",
		"GIT_TERMINAL_PROMPT=0",
		"HOME="+c.home,
		"XDG_CONFIG_HOME="+c.xdg,
		PublicationTokenEnvironment+"="+c.token,
	)
	fixed := []string{"-c", "credential.helper=", "-c", "credential.useHttpPath=true", "-c", "http.followRedirects=false"}
	return Command{Path: "git", Args: append(fixed, args...), Env: env, Dir: dir}, nil
}

func (c *GitHubGitCredentials) Close() error {
	if c == nil || c.directory == "" {
		return nil
	}
	directory := c.directory
	c.directory, c.askpass, c.home, c.xdg, c.token = "", "", "", "", ""
	return os.RemoveAll(directory)
}
