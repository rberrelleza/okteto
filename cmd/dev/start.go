// Copyright 2026 The Okteto Authors
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package dev

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/okteto/okteto/pkg/config"
	"github.com/okteto/okteto/pkg/constants"
	oktetoErrors "github.com/okteto/okteto/pkg/errors"
	oktetoLog "github.com/okteto/okteto/pkg/log"
	"github.com/okteto/okteto/pkg/okteto"
	"github.com/spf13/afero"
	"github.com/spf13/cobra"
)

const (
	// defaultStartTimeout is the default time to wait for the session to become ready
	defaultStartTimeout = 5 * time.Minute

	// readinessPollInterval is how often the state file is checked while waiting
	readinessPollInterval = 500 * time.Millisecond

	// failureLogLines is how many lines of session output are shown on failure
	failureLogLines = 30

	// heartbeatInterval is how often progress is reported when the state doesn't change.
	// The deploy phase of 'okteto up' happens before the state file exists, so without
	// a heartbeat the caller can't tell a slow start from a hang.
	heartbeatInterval = 10 * time.Second

	// maxHeartbeatLineLength is the maximum length of the log line echoed by the heartbeat
	maxHeartbeatLineLength = 120
)

// startFlags are the flags of the 'okteto dev start' command
type startFlags struct {
	commonFlags
	envs    []string
	timeout time.Duration
	deploy  bool
	reset   bool
}

// Start boots a development session in the background and returns when it is ready
func Start(fs afero.Fs) *cobra.Command {
	flags := &startFlags{}
	cmd := &cobra.Command{
		Use:   "start [devContainer] [flags] -- [COMMAND [args...]]",
		Short: "Start a development session in the background and return when it is ready",
		Long: `Start a development session in the background and return when it is ready.

The session runs the same development environment as 'okteto up' (code sync,
port-forwards and your app command) but detached: the command returns as soon
as the session is ready instead of taking over the terminal. "Ready" means the
code is synced, the port-forwards are up and the app command was launched; it
does not mean the app finished booting.

Starting an already running session is a no-op. Check readiness with
'okteto dev status', read the app output with 'okteto dev logs', run one-off
commands with 'okteto exec' and tear it down with 'okteto dev stop'.`,
		Example: `# Start a development session for the 'api' dev container
okteto dev start api

# Start it overriding the command defined in the Okteto Manifest
okteto dev start api -- yarn start`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			env, err := resolveDevEnvironment(ctx, fs, &flags.commonFlags, positionalArgs(args, cmd.ArgsLenAtDash()), true)
			if err != nil {
				return err
			}

			command := []string{}
			if cmd.ArgsLenAtDash() > -1 {
				command = args[cmd.ArgsLenAtDash():]
			}

			return runStart(env, flags, command, cmd.Root().Name())
		},
	}
	flags.commonFlags.register(cmd)
	cmd.Flags().StringArrayVarP(&flags.envs, "env", "e", []string{}, "set environment variable in the Development Container")
	cmd.Flags().BoolVarP(&flags.deploy, "deploy", "d", false, "force the redeployment of your Development Environment")
	cmd.Flags().BoolVarP(&flags.reset, "reset", "", false, "reset the file synchronization service")
	cmd.Flags().DurationVarP(&flags.timeout, "timeout", "t", defaultStartTimeout, "maximum time to wait for the session to be ready")
	return cmd
}

// positionalArgs returns the args before the '--' separator
func positionalArgs(args []string, argsLenAtDash int) []string {
	if argsLenAtDash > -1 {
		return args[:argsLenAtDash]
	}
	return args
}

func runStart(env *devEnvironment, flags *startFlags, command []string, binaryName string) error {
	current := getStatus(env.namespace, env.name)
	switch current.Status {
	case devStatusReady:
		oktetoLog.Success("Development session for '%s' is already running", env.name)
		printSessionSummary(env.namespace, env.name)
		return nil
	case devStatusStarting:
		oktetoLog.Information("A development session for '%s' is already starting, waiting for it to be ready...", env.name)
		return waitForExistingSession(env, current.PID, flags.timeout)
	case devStatusFailed:
		// crashed or leftover session: clean it up and start fresh
		oktetoLog.Information("Cleaning up a stale development session for '%s'", env.name)
		cleanupSessionFiles(env.namespace, env.name)
	}

	if len(command) == 0 && env.dev.IsInteractive() {
		oktetoLog.Warning("The dev command for '%s' is an interactive shell, so the session will idle after start.\n    Pass the command to run your app with: okteto dev start %s -- <command>", env.name, env.name)
	}

	return spawnSession(env, buildUpArgs(env.name, flags, command), "", flags.timeout)
}

// spawnSession starts a detached 'okteto up' with the given arguments, records the
// session metadata and waits until the session is ready. When dir is set, the child
// runs from that directory instead of the current one.
func spawnSession(env *devEnvironment, upArgs []string, dir string, timeout time.Duration) error {
	binary, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to resolve the okteto binary path: %w", err)
	}

	logPath := logFilePath(env.namespace, env.name)
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("failed to create the session log file: %w", err)
	}
	defer func() {
		if err := logFile.Close(); err != nil {
			oktetoLog.Debugf("error closing session log file: %s", err)
		}
	}()

	devNull, err := os.Open(os.DevNull)
	if err != nil {
		return fmt.Errorf("failed to open %s: %w", os.DevNull, err)
	}
	defer func() {
		if err := devNull.Close(); err != nil {
			oktetoLog.Debugf("error closing %s: %s", os.DevNull, err)
		}
	}()

	child := exec.Command(binary, upArgs...)
	child.Stdin = devNull
	child.Stdout = logFile
	child.Stderr = logFile
	child.Dir = dir
	child.Env = append(os.Environ(), fmt.Sprintf("%s=dev-start", constants.OktetoOriginEnvVar))
	configureDetachedProcess(child)

	if err := child.Start(); err != nil {
		return fmt.Errorf("failed to start the development session: %w", err)
	}

	sessionDir := dir
	if sessionDir == "" {
		if wd, err := os.Getwd(); err == nil {
			sessionDir = wd
		}
	}
	s := &session{
		PID:       child.Process.Pid,
		Binary:    binary,
		DevName:   env.name,
		Namespace: env.namespace,
		Context:   okteto.GetContext().Name,
		LogFile:   logPath,
		Dir:       sessionDir,
		StartedAt: time.Now(),
		Args:      upArgs,
	}
	if err := saveSession(s); err != nil {
		oktetoLog.Infof("failed to save session metadata: %s", err)
	}

	// reap the child if it dies while we wait for readiness
	exited := make(chan error, 1)
	go func() {
		exited <- child.Wait()
	}()

	oktetoLog.Information("Starting development session for '%s' in namespace '%s'...", env.name, env.namespace)

	timing, err := waitForReady(env.namespace, env.name, exited, timeout)
	if err != nil {
		cleanupSessionFiles(env.namespace, env.name)
		return err
	}

	oktetoLog.Success("Development session for '%s' is ready in %s", env.name, timing)
	printSessionSummary(env.namespace, env.name)
	return nil
}

// buildUpArgs builds the argument list of the detached 'okteto up' process
func buildUpArgs(devName string, flags *startFlags, command []string) []string {
	args := []string{"up", devName, "--log-output", "plain"}
	if flags.manifestPath != "" {
		args = append(args, "--file", flags.manifestPath)
	}
	if flags.namespace != "" {
		args = append(args, "--namespace", flags.namespace)
	}
	if flags.k8sContext != "" {
		args = append(args, "--context", flags.k8sContext)
	}
	if flags.deploy {
		args = append(args, "--deploy")
	}
	if flags.reset {
		args = append(args, "--reset")
	}
	for _, e := range flags.envs {
		args = append(args, "--env", e)
	}
	if len(command) > 0 {
		args = append(args, "--")
		args = append(args, command...)
	}
	return args
}

// startPhases tracks when each phase of the session start was first observed, to
// report where the time went once the session is ready
type startPhases struct {
	start      time.Time
	activating time.Time // first state observed: dev-container activation began (deploy/build done)
	syncing    time.Time // file synchronization setup began
	ready      time.Time
}

// summary renders the total time with a per-phase breakdown, e.g.
// "82s (deploy 35s, container 40s, sync 7s)". Phases that weren't observed are omitted.
func (p startPhases) summary() string {
	total := p.ready.Sub(p.start).Round(time.Second)
	segments := []string{}
	if !p.activating.IsZero() {
		segments = append(segments, fmt.Sprintf("deploy %s", p.activating.Sub(p.start).Round(time.Second)))
		syncStart := p.syncing
		if syncStart.IsZero() {
			syncStart = p.ready
		}
		segments = append(segments, fmt.Sprintf("container %s", syncStart.Sub(p.activating).Round(time.Second)))
		if !p.syncing.IsZero() {
			segments = append(segments, fmt.Sprintf("sync %s", p.ready.Sub(p.syncing).Round(time.Second)))
		}
	}
	if len(segments) == 0 {
		return total.String()
	}
	return fmt.Sprintf("%s (%s)", total, strings.Join(segments, ", "))
}

// waitForReady polls the session state until it is ready, the process exits or the
// timeout expires. It never leaves the caller hanging: on timeout the session is
// stopped and an error is returned, and while waiting it reports progress at least
// every heartbeatInterval. On success it returns a timing breakdown of the start.
func waitForReady(namespace, devName string, exited chan error, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(readinessPollInterval)
	defer ticker.Stop()

	phases := startPhases{start: time.Now()}
	lastState := ""
	lastProgressAt := time.Now()
	for {
		select {
		case <-exited:
			return "", sessionFailedError(namespace, devName, "the development session exited before becoming ready")
		case <-ticker.C:
			state := readUpState(namespace, devName)
			if state != lastState && state != "" {
				oktetoLog.Information("Session state: %s", state)
				lastState = state
				lastProgressAt = time.Now()
				if phases.activating.IsZero() {
					phases.activating = time.Now()
				}
				if phases.syncing.IsZero() && (state == config.StartingSync || state == config.Synchronizing) {
					phases.syncing = time.Now()
				}
			}
			switch status, _ := summarizeState(state); status {
			case devStatusReady:
				phases.ready = time.Now()
				return phases.summary(), nil
			case devStatusFailed:
				return "", sessionFailedError(namespace, devName, "the development session failed to start")
			}
			if time.Since(lastProgressAt) >= heartbeatInterval {
				elapsed := time.Since(phases.start).Round(time.Second)
				if line := lastLogLine(logFilePath(namespace, devName)); line != "" {
					oktetoLog.Information("Still starting (%s), last output: %s", elapsed, line)
				} else {
					oktetoLog.Information("Still starting (%s)", elapsed)
				}
				lastProgressAt = time.Now()
			}
			if time.Now().After(deadline) {
				if s := loadSession(namespace, devName); s != nil {
					terminateProcess(s.PID)
				}
				return "", sessionFailedError(namespace, devName, fmt.Sprintf("the development session wasn't ready after %s", timeout))
			}
		}
	}
}

// ansiEscapes matches ANSI escape sequences and other control characters that shell
// prompts and spinners write into the session log
var ansiEscapes = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]|\x1b[\]()][^\x07\x1b]*(\x07)?|[\x00-\x08\x0b-\x1f]`)

// lastLogLine returns the last non-empty line of the session log, cleaned up and
// truncated so it fits in a single heartbeat message
func lastLogLine(path string) string {
	tail, err := tailFile(path, 1)
	if err != nil {
		return ""
	}
	line := ansiEscapes.ReplaceAllString(tail, "")
	line = strings.TrimSpace(line)
	if len(line) > maxHeartbeatLineLength {
		line = line[:maxHeartbeatLineLength] + "..."
	}
	return line
}

// waitForExistingSession waits for a session started by another 'okteto dev start' to be ready
func waitForExistingSession(env *devEnvironment, pid int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(readinessPollInterval)
	defer ticker.Stop()

	for {
		<-ticker.C
		current := getStatus(env.namespace, env.name)
		switch current.Status {
		case devStatusReady:
			oktetoLog.Success("Development session for '%s' is ready", env.name)
			printSessionSummary(env.namespace, env.name)
			return nil
		case devStatusFailed, devStatusStopped:
			return sessionFailedError(env.namespace, env.name, "the development session failed to start")
		}
		if time.Now().After(deadline) {
			return sessionFailedError(env.namespace, env.name, fmt.Sprintf("the development session wasn't ready after %s", timeout))
		}
	}
}

// sessionFailedError builds the error shown when a session fails, including the last
// lines of its output so the failure is actionable without extra commands
func sessionFailedError(namespace, devName, reason string) error {
	logPath := logFilePath(namespace, devName)
	if tail, err := tailFile(logPath, failureLogLines); err == nil && tail != "" {
		oktetoLog.Println(fmt.Sprintf("--- last lines of %s ---", logPath))
		oktetoLog.Println(tail)
		oktetoLog.Println("---")
	}
	return oktetoErrors.UserError{
		E:    fmt.Errorf("%s", reason),
		Hint: fmt.Sprintf("Inspect '%s' and '%s' for details", logPath, appLogPath(namespace, devName)),
	}
}

// printSessionSummary prints where to go next after a session is running
func printSessionSummary(namespace, devName string) {
	oktetoLog.Println(fmt.Sprintf("    %s '%s' streams the app output", oktetoLog.BlueString("okteto dev logs"), devName))
	oktetoLog.Println(fmt.Sprintf("    %s '%s' reports readiness", oktetoLog.BlueString("okteto dev status"), devName))
	oktetoLog.Println(fmt.Sprintf("    %s '%s' tears the session down", oktetoLog.BlueString("okteto dev stop"), devName))
	oktetoLog.Println(fmt.Sprintf("    Log file: %s", logFilePath(namespace, devName)))
}
