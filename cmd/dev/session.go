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
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	ps "github.com/mitchellh/go-ps"
	"github.com/okteto/okteto/pkg/config"
)

const (
	// devLogFilename is the file where the detached 'okteto up' output is written
	devLogFilename = "okteto.dev.log"
	// devSessionFilename stores the metadata of a session started by 'okteto dev start'
	devSessionFilename = "okteto.dev.json"
	// upPIDFilename is the pid file written by 'okteto up' (see cmd/up/pid.go)
	upPIDFilename = "okteto.pid"
	// upStateFilename is the state file written by 'okteto up' (see pkg/config)
	upStateFilename = "okteto.state"
)

// exit codes of 'okteto dev status'. They are part of the command contract:
// automated callers branch on them instead of parsing output.
const (
	exitCodeReady     = 0
	exitCodeFailed    = 1
	exitCodeNotReady  = 3
	exitCodeNoSession = 4
)

// devStatus is the summarized status of a development session
type devStatus string

const (
	devStatusReady    devStatus = "ready"
	devStatusStarting devStatus = "starting"
	devStatusFailed   devStatus = "failed"
	devStatusStopped  devStatus = "stopped"
)

// session is the metadata of a development session started by 'okteto dev start'
type session struct {
	StartedAt time.Time `json:"startedAt"`
	Binary    string    `json:"binary"`
	DevName   string    `json:"devName"`
	Namespace string    `json:"namespace"`
	Context   string    `json:"context"`
	LogFile   string    `json:"logFile"`
	Dir       string    `json:"dir,omitempty"`
	Args      []string  `json:"args"`
	PID       int       `json:"pid"`
}

// statusInfo is the full status of a development session as reported by 'okteto dev status'
type statusInfo struct {
	StartedAt *time.Time `json:"startedAt,omitempty"`
	Name      string     `json:"name"`
	Namespace string     `json:"namespace"`
	Status    devStatus  `json:"status"`
	State     string     `json:"state,omitempty"`
	Detail    string     `json:"detail,omitempty"`
	LogFile   string     `json:"logFile,omitempty"`
	PID       int        `json:"pid,omitempty"`
	ExitCode  int        `json:"exitCode"`
}

func sessionFilePath(namespace, devName string) string {
	return filepath.Join(config.GetAppHome(namespace, devName), devSessionFilename)
}

func logFilePath(namespace, devName string) string {
	return filepath.Join(config.GetAppHome(namespace, devName), devLogFilename)
}

func upPIDFilePath(namespace, devName string) string {
	return filepath.Join(config.GetAppHome(namespace, devName), upPIDFilename)
}

func upStateFilePath(namespace, devName string) string {
	return filepath.Join(config.GetAppHome(namespace, devName), upStateFilename)
}

// loadSession returns the session metadata for the given dev environment or nil if there is none
func loadSession(namespace, devName string) *session {
	bytes, err := os.ReadFile(sessionFilePath(namespace, devName))
	if err != nil {
		return nil
	}
	var s session
	if err := json.Unmarshal(bytes, &s); err != nil {
		return nil
	}
	return &s
}

// saveSession persists the session metadata for the given dev environment
func saveSession(s *session) error {
	bytes, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(sessionFilePath(s.Namespace, s.DevName), bytes, 0600)
}

// removeSession removes the session metadata for the given dev environment
func removeSession(namespace, devName string) {
	_ = os.Remove(sessionFilePath(namespace, devName))
}

// readUpPID returns the pid written by 'okteto up' or 0 if there is none
func readUpPID(namespace, devName string) int {
	bytes, err := os.ReadFile(upPIDFilePath(namespace, devName))
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(bytes)))
	if err != nil {
		return 0
	}
	return pid
}

// readUpState returns the raw content of the 'okteto up' state file or "" if there is none.
// Unlike config.GetState, a missing file is not an error: it means there is no session.
func readUpState(namespace, devName string) string {
	bytes, err := os.ReadFile(upStateFilePath(namespace, devName))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(bytes))
}

// isProcessAlive returns true if a process with the given pid is running and looks like
// an okteto process. The name check protects against pid reuse reporting a false "ready".
func isProcessAlive(pid int, executable string) bool {
	if pid <= 0 {
		return false
	}
	proc, err := ps.FindProcess(pid)
	if err != nil || proc == nil {
		return false
	}
	name := strings.ToLower(proc.Executable())
	if executable != "" && strings.EqualFold(proc.Executable(), executable) {
		return true
	}
	return strings.Contains(name, "okteto")
}

// getStatus computes the status of the dev environment from the session metadata,
// the 'okteto up' pid file and the 'okteto up' state file. A crashed or leftover
// session is reported as failed, never as a false "ready".
func getStatus(namespace, devName string) statusInfo {
	info := statusInfo{
		Name:      devName,
		Namespace: namespace,
	}

	s := loadSession(namespace, devName)
	state := readUpState(namespace, devName)

	if s != nil {
		info.PID = s.PID
		info.LogFile = s.LogFile
		startedAt := s.StartedAt
		info.StartedAt = &startedAt
		if isProcessAlive(s.PID, filepath.Base(s.Binary)) {
			info.Status, info.ExitCode = summarizeState(state)
			info.State = state
			return info
		}
		info.Status = devStatusFailed
		info.ExitCode = exitCodeFailed
		info.Detail = "the development session process is not running anymore (crashed or killed)"
		return info
	}

	// no session started by 'okteto dev start': check for an 'okteto up' run outside of it
	upPID := readUpPID(namespace, devName)
	if upPID > 0 {
		info.PID = upPID
		if isProcessAlive(upPID, "") {
			info.Status, info.ExitCode = summarizeState(state)
			info.State = state
			return info
		}
		info.Status = devStatusFailed
		info.ExitCode = exitCodeFailed
		info.Detail = "'okteto up' left a stale session behind (crashed or killed)"
		return info
	}

	if state != "" {
		info.Status = devStatusFailed
		info.ExitCode = exitCodeFailed
		info.State = state
		info.Detail = "a previous session didn't shut down cleanly"
		return info
	}

	info.Status = devStatusStopped
	info.ExitCode = exitCodeNoSession
	return info
}

// summarizeState maps the raw 'okteto up' state to a summarized status and exit code.
// "ready" means code synced, forwards up and the app command launched; it does not
// mean the app finished booting.
func summarizeState(state string) (devStatus, int) {
	switch config.UpState(state) {
	case config.Ready:
		return devStatusReady, exitCodeReady
	case config.Failed:
		return devStatusFailed, exitCodeFailed
	default:
		// includes the empty state right after spawning the session
		return devStatusStarting, exitCodeNotReady
	}
}

// findSessions returns the sessions recorded for the given dev container name across
// all namespaces. Paths are probed directly (not through GetAppHome) so scanning
// doesn't create directories as a side effect.
func findSessions(devName string) []*session {
	home := config.GetOktetoHome()
	entries, err := os.ReadDir(home)
	if err != nil {
		return nil
	}
	var sessions []*session
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		bytes, err := os.ReadFile(filepath.Join(home, entry.Name(), devName, devSessionFilename))
		if err != nil {
			continue
		}
		var s session
		if err := json.Unmarshal(bytes, &s); err != nil {
			continue
		}
		if s.DevName == devName {
			sessions = append(sessions, &s)
		}
	}
	return sessions
}

// hasSessionFiles returns true if the dev environment has a session or a running
// 'okteto up' recorded in the given namespace
func hasSessionFiles(namespace, devName string) bool {
	return loadSession(namespace, devName) != nil || readUpPID(namespace, devName) > 0
}

// cleanupSessionFiles removes the session metadata and, when no live process owns them,
// the leftover 'okteto up' pid and state files
func cleanupSessionFiles(namespace, devName string) {
	removeSession(namespace, devName)
	if pid := readUpPID(namespace, devName); pid > 0 && isProcessAlive(pid, "") {
		return
	}
	_ = os.Remove(upPIDFilePath(namespace, devName))
	_ = os.Remove(upStateFilePath(namespace, devName))
}
