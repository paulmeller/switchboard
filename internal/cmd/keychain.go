package cmd

import (
	"context"
	"io"
	"os/exec"
	"time"
)

// keychainPresent discards attributes and never requests a password (-w/-g).
func keychainPresent(service string) bool {
	if service != "claude-oauth-work" && service != "claude-oauth-personal" {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "security", "find-generic-password", "-s", service)
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	cmd.WaitDelay = 100 * time.Millisecond
	return cmd.Run() == nil
}
