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
	"time"

	oktetoLog "github.com/okteto/okteto/pkg/log"
	"github.com/spf13/afero"
	"github.com/spf13/cobra"
)

const (
	// defaultStopTimeout is how long to wait for a graceful shutdown before killing
	defaultStopTimeout = 30 * time.Second

	// stopPollInterval is how often the process is checked while stopping
	stopPollInterval = 200 * time.Millisecond

	// forceKillWait is how long to wait for the process to die after a force kill
	forceKillWait = 5 * time.Second
)

// stopFlags are the flags of the 'okteto dev stop' command
type stopFlags struct {
	commonFlags
	timeout time.Duration
}

// Stop tears down a development session. It is idempotent: stopping a stopped session succeeds.
func Stop(fs afero.Fs) *cobra.Command {
	flags := &stopFlags{}
	cmd := &cobra.Command{
		Use:   "stop [devContainer] [flags]",
		Short: "Stop a development session",
		Long: `Stop a development session started with 'okteto dev start'.

The session process is asked to shut down cleanly (stopping the file
synchronization and the port-forwards) and is killed if it doesn't finish in
time. Stopping a session that isn't running is not an error, so the command is
safe to call twice. Leftover session files from a crashed session are cleaned
up too.

This stops the session but keeps the Development Container deployed; run
'okteto down' to also restore the original deployment.`,
		Example: `# Stop the development session of the 'api' dev container
okteto dev stop api`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			env, err := resolveDevEnvironment(ctx, fs, &flags.commonFlags, args, false)
			if err != nil {
				return err
			}
			env.namespace, err = resolveSessionNamespace(env, flags.namespace)
			if err != nil {
				return err
			}
			return runStop(env, flags.timeout)
		},
	}
	flags.commonFlags.register(cmd)
	cmd.Flags().DurationVarP(&flags.timeout, "timeout", "t", defaultStopTimeout, "maximum time to wait for a graceful shutdown before killing the session")
	return cmd
}

func runStop(env *devEnvironment, timeout time.Duration) error {
	target := 0
	if s := loadSession(env.namespace, env.name); s != nil && isProcessAlive(s.PID, "") {
		target = s.PID
	} else if pid := readUpPID(env.namespace, env.name); pid > 0 && isProcessAlive(pid, "") {
		// also stop sessions started by a plain 'okteto up'
		target = pid
	}

	if target == 0 {
		cleanupSessionFiles(env.namespace, env.name)
		oktetoLog.Success("No development session running for '%s', nothing to stop", env.name)
		return nil
	}

	oktetoLog.Information("Stopping development session for '%s'...", env.name)
	if err := terminateProcess(target); err != nil {
		oktetoLog.Infof("failed to signal process %d: %s", target, err)
	}

	if waitForProcessExit(target, timeout) {
		cleanupSessionFiles(env.namespace, env.name)
		oktetoLog.Success("Development session for '%s' stopped", env.name)
		return nil
	}

	oktetoLog.Information("The session didn't shut down after %s, killing it...", timeout)
	if err := killProcess(target); err != nil {
		oktetoLog.Infof("failed to kill process %d: %s", target, err)
	}

	if !waitForProcessExit(target, forceKillWait) {
		return fmt.Errorf("couldn't stop the development session process (pid %d)", target)
	}

	cleanupSessionFiles(env.namespace, env.name)
	oktetoLog.Success("Development session for '%s' stopped", env.name)
	return nil
}

// waitForProcessExit polls until the process is gone or the timeout expires
func waitForProcessExit(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(stopPollInterval)
	defer ticker.Stop()

	for {
		if !isProcessAlive(pid, "") {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		<-ticker.C
	}
}
