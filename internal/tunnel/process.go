// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package tunnel

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"syscall"
	"time"
)

const processTerminationTimeout = 5 * time.Second

// ProcessRunner directly executes fixed locally configured tunnel processes.
type ProcessRunner struct{ definitions map[string]ProcessConfig }

// NewProcessRunner creates a process runner from trusted local definitions.
func NewProcessRunner(definitions map[string]ProcessConfig) *ProcessRunner {
	return &ProcessRunner{definitions: definitions}
}

// Run executes a tunnel without a shell or inherited environment and probes readiness.
func (r *ProcessRunner) Run(ctx context.Context, tunnelName string, ready func()) error {
	definition, ok := r.definitions[tunnelName]
	if !ok {
		return fmt.Errorf("unknown tunnel name %q", tunnelName)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, definition.ReadinessURL, nil)
	if err != nil {
		return fmt.Errorf("create readiness request: %w", err)
	}
	command := exec.Command(definition.Command, definition.Args...)
	command.Env = []string{}
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := command.Start(); err != nil {
		return fmt.Errorf("start tunnel: %w", err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	deadline := time.NewTimer(definition.ReadinessTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	client := &http.Client{Timeout: time.Second}
	for {
		select {
		case err := <-done:
			if err != nil {
				return fmt.Errorf("tunnel exited: %w", err)
			}
			return fmt.Errorf("tunnel exited")
		case <-deadline.C:
			terminateProcess(command.Process, done)
			return fmt.Errorf("readiness timeout")
		case <-ticker.C:
			response, err := client.Do(request)
			if err == nil {
				_ = response.Body.Close()
				if response.StatusCode >= 200 && response.StatusCode < 300 {
					ready()
					select {
					case err := <-done:
						return err
					case <-ctx.Done():
						terminateProcess(command.Process, done)
						return ctx.Err()
					}
				}
			}
		case <-ctx.Done():
			terminateProcess(command.Process, done)
			return ctx.Err()
		}
	}
}

// terminateProcess gracefully stops a process group before forcing its exit.
func terminateProcess(process *os.Process, done <-chan error) {
	if err := syscall.Kill(-process.Pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		_ = syscall.Kill(-process.Pid, syscall.SIGKILL)
	}
	timer := time.NewTimer(processTerminationTimeout)
	defer timer.Stop()
	select {
	case <-done:
		return
	case <-timer.C:
		_ = syscall.Kill(-process.Pid, syscall.SIGKILL)
		<-done
	}
}
