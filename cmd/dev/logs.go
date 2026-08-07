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
	"io"
	"os"
	"strings"
	"time"

	oktetoErrors "github.com/okteto/okteto/pkg/errors"
	oktetoLog "github.com/okteto/okteto/pkg/log"
	"github.com/spf13/afero"
	"github.com/spf13/cobra"
)

const (
	// defaultLogsTail is the default number of lines shown by 'okteto dev logs'
	defaultLogsTail = 100

	// watchPollInterval is how often the log file is checked for new output with --watch
	watchPollInterval = 300 * time.Millisecond

	// watchIdleChecks is how many idle polls with a dead session end the watch
	watchIdleChecks = 5
)

// logsFlags are the flags of the 'okteto dev logs' command
type logsFlags struct {
	commonFlags
	tail  int
	watch bool
}

// Logs streams the output of a development session
func Logs(fs afero.Fs) *cobra.Command {
	flags := &logsFlags{}
	cmd := &cobra.Command{
		Use:   "logs [devContainer] [flags]",
		Short: "Print the output of a development session",
		Long: `Print the output of a development session started with 'okteto dev start'.

Without flags it prints the last lines of output and returns. With '--watch'
it keeps streaming new output until interrupted or until the session ends.`,
		Example: `# Print the last 100 lines of the session output
okteto dev logs api

# Print the whole session output
okteto dev logs api --tail -1

# Stream the session output
okteto dev logs api --watch`,
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

			logPath := logFilePath(env.namespace, env.name)
			if _, err := os.Stat(logPath); err != nil {
				return oktetoErrors.ExitError{
					Code: exitCodeNoSession,
					Err: oktetoErrors.UserError{
						E:    fmt.Errorf("no development session logs found for '%s'", env.name),
						Hint: "Run 'okteto dev start' to start a development session",
					},
				}
			}

			if flags.watch {
				return followLogs(env.namespace, env.name, logPath, flags.tail)
			}

			tail, err := tailFile(logPath, flags.tail)
			if err != nil {
				return err
			}
			if tail != "" {
				fmt.Println(tail)
			}
			return nil
		},
	}
	flags.commonFlags.register(cmd)
	cmd.Flags().IntVarP(&flags.tail, "tail", "", defaultLogsTail, "number of lines to show from the end of the logs (-1 for all)")
	cmd.Flags().BoolVarP(&flags.watch, "watch", "w", false, "keep streaming new output")
	return cmd
}

// tailFile returns the last n lines of the file (all lines when n < 0)
func tailFile(path string, n int) (string, error) {
	bytes, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	content := strings.TrimRight(string(bytes), "\n")
	if content == "" || n < 0 {
		return content, nil
	}
	lines := strings.Split(content, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n"), nil
}

// followLogs prints the tail of the log file and keeps streaming new output. It ends
// when interrupted or when the session is gone and no new output shows up.
func followLogs(namespace, devName, path string, tail int) error {
	initial, err := tailFile(path, tail)
	if err != nil {
		return err
	}
	if initial != "" {
		fmt.Println(initial)
	}

	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() {
		if err := f.Close(); err != nil {
			oktetoLog.Debugf("error closing log file: %s", err)
		}
	}()

	offset, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		return err
	}

	ticker := time.NewTicker(watchPollInterval)
	defer ticker.Stop()

	idleWithDeadSession := 0
	for {
		<-ticker.C
		info, err := f.Stat()
		if err != nil {
			return err
		}
		if info.Size() > offset {
			if _, err := f.Seek(offset, io.SeekStart); err != nil {
				return err
			}
			written, err := io.Copy(os.Stdout, f)
			if err != nil {
				return err
			}
			offset += written
			idleWithDeadSession = 0
			continue
		}

		if getStatus(namespace, devName).Status == devStatusStarting || sessionProcessAlive(namespace, devName) {
			continue
		}
		idleWithDeadSession++
		if idleWithDeadSession >= watchIdleChecks {
			oktetoLog.Information("The development session for '%s' is not running anymore", devName)
			return nil
		}
	}
}

// sessionProcessAlive returns true if a live process owns the session of the dev environment
func sessionProcessAlive(namespace, devName string) bool {
	if s := loadSession(namespace, devName); s != nil && isProcessAlive(s.PID, "") {
		return true
	}
	if pid := readUpPID(namespace, devName); pid > 0 && isProcessAlive(pid, "") {
		return true
	}
	return false
}
