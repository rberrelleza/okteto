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
	"encoding/json"
	"fmt"
	"time"

	oktetoErrors "github.com/okteto/okteto/pkg/errors"
	oktetoLog "github.com/okteto/okteto/pkg/log"
	"github.com/spf13/afero"
	"github.com/spf13/cobra"
)

// statusFlags are the flags of the 'okteto dev status' command
type statusFlags struct {
	commonFlags
	output  string
	timeout time.Duration
	wait    bool
}

// Status reports the state of a development session in a machine-readable way
func Status(fs afero.Fs) *cobra.Command {
	flags := &statusFlags{}
	cmd := &cobra.Command{
		Use:   "status [devContainer] [flags]",
		Short: "Report the state of a development session (machine readable)",
		Long: `Report the state of a development session.

The exit code is part of the contract, so automated callers can branch on it
without parsing output:

  0  ready: code synced, port-forwards up, app command launched
  1  failed: the session failed, crashed or was left behind
  3  not ready yet: the session is still starting
  4  no session running

"Ready" does not mean the app finished booting; check 'okteto dev logs' for
your app's own readiness output. A crashed or leftover session is reported as
failed, never as a false "ready".`,
		Example: `# Check the session state
okteto dev status api

# Get it as JSON
okteto dev status api -o json

# Block until the session is ready or failed
okteto dev status api --wait`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if flags.output != "" && flags.output != "json" {
				return fmt.Errorf("unsupported output format %q: only 'json' is supported", flags.output)
			}
			env, err := resolveDevEnvironment(ctx, fs, &flags.commonFlags, args, false)
			if err != nil {
				return err
			}
			env.namespace, err = resolveSessionNamespace(env, flags.namespace)
			if err != nil {
				return err
			}

			info := getStatus(env.namespace, env.name)
			if flags.wait {
				info = waitForTerminalStatus(env.namespace, env.name, flags.timeout)
			}

			if flags.output == "json" {
				bytes, err := json.MarshalIndent(info, "", "  ")
				if err != nil {
					return err
				}
				fmt.Println(string(bytes))
			} else {
				printStatus(info)
			}

			if info.ExitCode != 0 {
				// the state was already reported; the error only carries the exit code
				return oktetoErrors.ExitError{Code: info.ExitCode}
			}
			return nil
		},
	}
	flags.commonFlags.register(cmd)
	cmd.Flags().StringVarP(&flags.output, "output", "o", "", "output format (json)")
	cmd.Flags().BoolVarP(&flags.wait, "wait", "w", false, "wait until the session is ready or failed")
	cmd.Flags().DurationVarP(&flags.timeout, "timeout", "t", defaultStartTimeout, "maximum time to wait when --wait is set")
	return cmd
}

// waitForTerminalStatus polls the session status until it is ready, failed or gone,
// or until the timeout expires. On timeout it returns the current status, so the
// caller is never left hanging.
func waitForTerminalStatus(namespace, devName string, timeout time.Duration) statusInfo {
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(readinessPollInterval)
	defer ticker.Stop()

	for {
		info := getStatus(namespace, devName)
		if info.Status != devStatusStarting || time.Now().After(deadline) {
			return info
		}
		<-ticker.C
	}
}

// printStatus prints the status for humans: one line with the summary, plus detail lines
func printStatus(info statusInfo) {
	switch info.Status {
	case devStatusReady:
		oktetoLog.Success("Development session for '%s' is ready", info.Name)
	case devStatusStarting:
		oktetoLog.Information("Development session for '%s' is starting (state: %s)", info.Name, info.State)
	case devStatusFailed:
		oktetoLog.Warning("Development session for '%s' failed", info.Name)
		if info.Detail != "" {
			oktetoLog.Println(fmt.Sprintf("    %s", info.Detail))
		}
		if info.LogFile != "" {
			oktetoLog.Println(fmt.Sprintf("    Log file: %s", info.LogFile))
		}
	case devStatusStopped:
		oktetoLog.Information("No development session running for '%s'", info.Name)
	}
}
