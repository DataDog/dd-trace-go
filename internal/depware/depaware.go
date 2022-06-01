// Package depware maintains a dependency on github.com/tailscale/depware so it
// doesn't get removed from go.mod by "go mod tidy". This lets us run depware
// with "go run github.com/tailscale/depaware" from CI or locally

package depware

import (
	_ "github.com/tailscale/depaware/depaware"
)
