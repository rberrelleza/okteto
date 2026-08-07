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
	"strings"
	"time"

	oktetoLog "github.com/okteto/okteto/pkg/log"
	"github.com/spf13/afero"
	"github.com/spf13/cobra"
)

// restartFlags are the flags of the 'okteto dev restart' command
type restartFlags struct {
	commonFlags
	timeout time.Duration
	reset   bool
}

// Restart stops a development session and starts it again with the same configuration
func Restart(fs afero.Fs) *cobra.Command {
	flags := &restartFlags{}
	cmd := &cobra.Command{
		Use:   "restart [devContainer] [flags]",
		Short: "Restart a development session",
		Long: `Restart a development session.

The session is stopped and started again with the same configuration and
command it was originally started with, re-running your app command inside the
Development Container. If no session is running, one is started.

Use '--reset' to also reset the file synchronization service, e.g. when the
sync got into a bad state. To restart with different options or a different
command, use 'okteto dev stop' followed by 'okteto dev start' instead.`,
		Example: `# Restart the development session of the 'api' dev container
okteto dev restart api

# Restart it and reset the file synchronization service
okteto dev restart api --reset`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			env, err := resolveDevEnvironment(ctx, fs, &flags.commonFlags, args, true)
			if err != nil {
				return err
			}
			env.namespace, err = resolveSessionNamespace(env, flags.namespace)
			if err != nil {
				return err
			}

			// read the recorded configuration before stop removes the session file
			prev := loadSession(env.namespace, env.name)

			if prev != nil || readUpPID(env.namespace, env.name) > 0 {
				if err := runStop(env, defaultStopTimeout); err != nil {
					return err
				}
			} else {
				oktetoLog.Information("No development session running for '%s', starting a new one", env.name)
			}

			upArgs, dir := buildRestartArgs(env.name, prev, flags)

			if !hasCommandOverride(upArgs) && env.dev.IsInteractive() {
				oktetoLog.Warning("The dev command for '%s' is an interactive shell, so the session will idle after start.\n    Pass the command to run your app with: okteto dev start %s -- <command>", env.name, env.name)
			}

			return spawnSession(env, upArgs, dir, flags.timeout)
		},
	}
	flags.commonFlags.register(cmd)
	cmd.Flags().BoolVarP(&flags.reset, "reset", "", false, "also reset the file synchronization service")
	cmd.Flags().DurationVarP(&flags.timeout, "timeout", "t", defaultStartTimeout, "maximum time to wait for the session to be ready")
	return cmd
}

// buildRestartArgs returns the 'okteto up' arguments and working directory for the
// restarted session. When the previous session recorded its arguments they are reused,
// pinning the namespace and context it was started in so the session restarts where it
// lives even if the current context changed. Otherwise the arguments are built fresh.
func buildRestartArgs(devName string, prev *session, flags *restartFlags) ([]string, string) {
	if prev == nil || len(prev.Args) == 0 {
		startFlags := &startFlags{commonFlags: flags.commonFlags, reset: flags.reset}
		return buildUpArgs(devName, startFlags, nil), ""
	}

	extra := []string{}
	if prev.Namespace != "" && !upFlagPresent(prev.Args, "--namespace", "-n") {
		extra = append(extra, "--namespace", prev.Namespace)
	}
	if prev.Context != "" && !upFlagPresent(prev.Args, "--context", "-c") {
		extra = append(extra, "--context", prev.Context)
	}
	if flags.reset && !upFlagPresent(prev.Args, "--reset") {
		extra = append(extra, "--reset")
	}
	return insertUpArgs(prev.Args, extra...), prev.Dir
}

// upFlagPresent returns true if any of the given flags appears in the 'okteto up'
// arguments before the '--' command separator
func upFlagPresent(args []string, names ...string) bool {
	for _, arg := range args {
		if arg == "--" {
			return false
		}
		for _, name := range names {
			if arg == name || strings.HasPrefix(arg, name+"=") {
				return true
			}
		}
	}
	return false
}

// insertUpArgs adds arguments to the 'okteto up' invocation, keeping them before the
// '--' command separator when one is present. The input slice is not modified.
func insertUpArgs(args []string, extra ...string) []string {
	out := make([]string, 0, len(args)+len(extra))
	for i, arg := range args {
		if arg == "--" {
			out = append(out, args[:i]...)
			out = append(out, extra...)
			out = append(out, args[i:]...)
			return out
		}
	}
	out = append(out, args...)
	out = append(out, extra...)
	return out
}

// hasCommandOverride returns true if the 'okteto up' arguments carry a command after
// the '--' separator
func hasCommandOverride(args []string) bool {
	for _, arg := range args {
		if arg == "--" {
			return true
		}
	}
	return false
}
