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
	"time"

	oktetoErrors "github.com/okteto/okteto/pkg/errors"
	oktetoLog "github.com/okteto/okteto/pkg/log"
	"github.com/okteto/okteto/pkg/okteto"
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
	volumes bool
}

// Stop tears down a development session. It is idempotent: stopping a stopped session succeeds.
func Stop(fs afero.Fs) *cobra.Command {
	flags := &stopFlags{}
	cmd := &cobra.Command{
		Use:   "stop [devContainer] [flags]",
		Short: "Stop a development session and deactivate the Development Container",
		Long: `Stop a development session started with 'okteto dev start'.

The session process is asked to shut down cleanly (stopping the file
synchronization and the port-forwards) and is killed if it doesn't finish in
time. Then development mode is deactivated ('okteto down'), restoring the
original deployment. Stopping a session that isn't running is not an error,
so the command is safe to call twice; leftover files from a crashed session
are cleaned up too.

Use '--volumes' to also remove the persistent volume used to speed up
subsequent starts.`,
		Example: `# Stop the development session of the 'api' dev container
okteto dev stop api

# Stop it and remove its persistent volume
okteto dev stop api --volumes`,
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
			// remember the context the session was started on before stop cleans its metadata
			sessionContext := ""
			if s := loadSession(env.namespace, env.name); s != nil {
				sessionContext = s.Context
			}
			if err := runStop(env, flags.timeout); err != nil {
				return err
			}
			return runDown(env, flags, sessionContext)
		},
	}
	flags.commonFlags.register(cmd)
	cmd.Flags().DurationVarP(&flags.timeout, "timeout", "t", defaultStopTimeout, "maximum time to wait for a graceful shutdown before killing the session")
	cmd.Flags().BoolVarP(&flags.volumes, "volumes", "v", false, "also remove the persistent volume of the Development Container")
	return cmd
}

// runStop terminates the session process and cleans up the session files. It doesn't
// touch the cluster: deactivating development mode is runDown's job, so 'okteto dev
// restart' can reuse this for a fast bounce.
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
		oktetoLog.Information("No development session running for '%s'", env.name)
		return nil
	}

	oktetoLog.Information("Stopping development session for '%s'...", env.name)
	if err := terminateProcess(target); err != nil {
		oktetoLog.Infof("failed to signal process %d: %s", target, err)
	}

	if !waitForProcessExit(target, timeout) {
		oktetoLog.Information("The session didn't shut down after %s, killing it...", timeout)
		if err := killProcess(target); err != nil {
			oktetoLog.Infof("failed to kill process %d: %s", target, err)
		}
		if !waitForProcessExit(target, forceKillWait) {
			return fmt.Errorf("couldn't stop the development session process (pid %d)", target)
		}
	}

	cleanupSessionFiles(env.namespace, env.name)
	oktetoLog.Success("Development session for '%s' stopped", env.name)
	return nil
}

// runDown deactivates development mode by running 'okteto down' for the dev container,
// restoring the original deployment. Deactivating an already deactivated Development
// Container succeeds, keeping 'okteto dev stop' idempotent. The session's recorded
// context takes precedence, so the right cluster is targeted even after a context switch.
func runDown(env *devEnvironment, flags *stopFlags, sessionContext string) error {
	binary, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to resolve the okteto binary path: %w", err)
	}

	ctxName := sessionContext
	if ctxName == "" {
		ctxName = okteto.GetContext().Name
	}
	args := []string{"down", env.name, "--namespace", env.namespace, "--log-output", "plain"}
	if ctxName != "" {
		args = append(args, "--context", ctxName)
	}
	if flags.manifestPath != "" {
		args = append(args, "--file", flags.manifestPath)
	}
	if flags.volumes {
		args = append(args, "--volumes")
	}

	oktetoLog.Information("Deactivating development mode for '%s'...", env.name)
	down := exec.Command(binary, args...)
	down.Stdout = os.Stdout
	down.Stderr = os.Stderr
	if err := down.Run(); err != nil {
		return oktetoErrors.UserError{
			E:    fmt.Errorf("the session was stopped but 'okteto down' failed: %w", err),
			Hint: fmt.Sprintf("Run 'okteto down %s --namespace <namespace>' manually to deactivate the Development Container", env.name),
		}
	}
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
