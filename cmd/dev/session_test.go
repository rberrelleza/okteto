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
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/okteto/okteto/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const deadPID = 999999

func setupOktetoHome(t *testing.T) {
	t.Setenv("OKTETO_FOLDER", t.TempDir())
}

func writeState(t *testing.T, namespace, devName, state string) {
	require.NoError(t, os.WriteFile(upStateFilePath(namespace, devName), []byte(state), 0600))
}

func writeUpPID(t *testing.T, namespace, devName string, pid int) {
	require.NoError(t, os.WriteFile(upPIDFilePath(namespace, devName), []byte(strconv.Itoa(pid)), 0600))
}

func TestSummarizeState(t *testing.T) {
	tests := []struct {
		state        string
		expected     devStatus
		expectedCode int
	}{
		{state: string(config.Ready), expected: devStatusReady, expectedCode: exitCodeReady},
		{state: string(config.Failed), expected: devStatusFailed, expectedCode: exitCodeFailed},
		{state: string(config.Activating), expected: devStatusStarting, expectedCode: exitCodeNotReady},
		{state: config.Starting, expected: devStatusStarting, expectedCode: exitCodeNotReady},
		{state: config.Synchronizing, expected: devStatusStarting, expectedCode: exitCodeNotReady},
		{state: "", expected: devStatusStarting, expectedCode: exitCodeNotReady},
	}
	for _, tt := range tests {
		t.Run("state "+tt.state, func(t *testing.T) {
			status, code := summarizeState(tt.state)
			assert.Equal(t, tt.expected, status)
			assert.Equal(t, tt.expectedCode, code)
		})
	}
}

func TestGetStatusNoSession(t *testing.T) {
	setupOktetoHome(t)
	info := getStatus("ns", "api")
	assert.Equal(t, devStatusStopped, info.Status)
	assert.Equal(t, exitCodeNoSession, info.ExitCode)
}

func TestGetStatusReadySession(t *testing.T) {
	setupOktetoHome(t)
	binary, err := os.Executable()
	require.NoError(t, err)
	require.NoError(t, saveSession(&session{
		PID:       os.Getpid(),
		Binary:    binary,
		DevName:   "api",
		Namespace: "ns",
		LogFile:   logFilePath("ns", "api"),
		StartedAt: time.Now(),
	}))
	writeState(t, "ns", "api", string(config.Ready))

	info := getStatus("ns", "api")
	assert.Equal(t, devStatusReady, info.Status)
	assert.Equal(t, exitCodeReady, info.ExitCode)
	assert.Equal(t, os.Getpid(), info.PID)
}

func TestGetStatusStartingSession(t *testing.T) {
	setupOktetoHome(t)
	binary, err := os.Executable()
	require.NoError(t, err)
	require.NoError(t, saveSession(&session{
		PID:       os.Getpid(),
		Binary:    binary,
		DevName:   "api",
		Namespace: "ns",
		StartedAt: time.Now(),
	}))
	writeState(t, "ns", "api", config.Synchronizing)

	info := getStatus("ns", "api")
	assert.Equal(t, devStatusStarting, info.Status)
	assert.Equal(t, exitCodeNotReady, info.ExitCode)
}

func TestGetStatusCrashedSessionIsNeverReady(t *testing.T) {
	setupOktetoHome(t)
	require.NoError(t, saveSession(&session{
		PID:       deadPID,
		Binary:    "okteto",
		DevName:   "api",
		Namespace: "ns",
		StartedAt: time.Now(),
	}))
	// even with a leftover "ready" state, a dead process must be reported as failed
	writeState(t, "ns", "api", string(config.Ready))

	info := getStatus("ns", "api")
	assert.Equal(t, devStatusFailed, info.Status)
	assert.Equal(t, exitCodeFailed, info.ExitCode)
	assert.NotEmpty(t, info.Detail)
}

func TestGetStatusStaleUpSession(t *testing.T) {
	setupOktetoHome(t)
	writeUpPID(t, "ns", "api", deadPID)
	writeState(t, "ns", "api", string(config.Ready))

	info := getStatus("ns", "api")
	assert.Equal(t, devStatusFailed, info.Status)
	assert.Equal(t, exitCodeFailed, info.ExitCode)
}

func TestGetStatusLeftoverStateFile(t *testing.T) {
	setupOktetoHome(t)
	writeState(t, "ns", "api", config.Synchronizing)

	info := getStatus("ns", "api")
	assert.Equal(t, devStatusFailed, info.Status)
	assert.Equal(t, exitCodeFailed, info.ExitCode)
}

func TestCleanupSessionFiles(t *testing.T) {
	setupOktetoHome(t)
	require.NoError(t, saveSession(&session{
		PID:       deadPID,
		DevName:   "api",
		Namespace: "ns",
		StartedAt: time.Now(),
	}))
	writeState(t, "ns", "api", string(config.Ready))
	writeUpPID(t, "ns", "api", deadPID)

	cleanupSessionFiles("ns", "api")

	assert.NoFileExists(t, sessionFilePath("ns", "api"))
	assert.NoFileExists(t, upStateFilePath("ns", "api"))
	assert.NoFileExists(t, upPIDFilePath("ns", "api"))

	info := getStatus("ns", "api")
	assert.Equal(t, devStatusStopped, info.Status)
	assert.Equal(t, exitCodeNoSession, info.ExitCode)
}

func TestIsProcessAliveDeadPID(t *testing.T) {
	assert.False(t, isProcessAlive(deadPID, ""))
	assert.False(t, isProcessAlive(0, ""))
	assert.False(t, isProcessAlive(-1, ""))
}

func TestFindSessions(t *testing.T) {
	setupOktetoHome(t)
	require.NoError(t, saveSession(&session{PID: deadPID, DevName: "api", Namespace: "ns1", StartedAt: time.Now()}))
	require.NoError(t, saveSession(&session{PID: deadPID, DevName: "api", Namespace: "ns2", StartedAt: time.Now()}))
	require.NoError(t, saveSession(&session{PID: deadPID, DevName: "other", Namespace: "ns2", StartedAt: time.Now()}))

	sessions := findSessions("api")
	require.Len(t, sessions, 2)
	namespaces := []string{sessions[0].Namespace, sessions[1].Namespace}
	assert.ElementsMatch(t, []string{"ns1", "ns2"}, namespaces)

	assert.Len(t, findSessions("other"), 1)
	assert.Empty(t, findSessions("missing"))
}

func TestResolveSessionNamespace(t *testing.T) {
	t.Run("explicit namespace flag wins", func(t *testing.T) {
		setupOktetoHome(t)
		require.NoError(t, saveSession(&session{PID: deadPID, DevName: "api", Namespace: "other", StartedAt: time.Now()}))
		env := &devEnvironment{name: "api", namespace: "current"}
		ns, err := resolveSessionNamespace(env, "current")
		require.NoError(t, err)
		assert.Equal(t, "current", ns)
	})

	t.Run("session in current namespace wins over other namespaces", func(t *testing.T) {
		setupOktetoHome(t)
		require.NoError(t, saveSession(&session{PID: deadPID, DevName: "api", Namespace: "current", StartedAt: time.Now()}))
		require.NoError(t, saveSession(&session{PID: deadPID, DevName: "api", Namespace: "other", StartedAt: time.Now()}))
		env := &devEnvironment{name: "api", namespace: "current"}
		ns, err := resolveSessionNamespace(env, "")
		require.NoError(t, err)
		assert.Equal(t, "current", ns)
	})

	t.Run("single session in another namespace is found", func(t *testing.T) {
		setupOktetoHome(t)
		require.NoError(t, saveSession(&session{PID: deadPID, DevName: "api", Namespace: "other", StartedAt: time.Now()}))
		env := &devEnvironment{name: "api", namespace: "current"}
		ns, err := resolveSessionNamespace(env, "")
		require.NoError(t, err)
		assert.Equal(t, "other", ns)
	})

	t.Run("no sessions anywhere falls back to current namespace", func(t *testing.T) {
		setupOktetoHome(t)
		env := &devEnvironment{name: "api", namespace: "current"}
		ns, err := resolveSessionNamespace(env, "")
		require.NoError(t, err)
		assert.Equal(t, "current", ns)
	})

	t.Run("sessions in multiple namespaces require the namespace flag", func(t *testing.T) {
		setupOktetoHome(t)
		require.NoError(t, saveSession(&session{PID: deadPID, DevName: "api", Namespace: "ns1", StartedAt: time.Now()}))
		require.NoError(t, saveSession(&session{PID: deadPID, DevName: "api", Namespace: "ns2", StartedAt: time.Now()}))
		env := &devEnvironment{name: "api", namespace: "current"}
		_, err := resolveSessionNamespace(env, "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "multiple namespaces")
	})
}

func TestTailFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "log")
	require.NoError(t, os.WriteFile(path, []byte("one\ntwo\nthree\nfour\n"), 0600))

	tail, err := tailFile(path, 2)
	require.NoError(t, err)
	assert.Equal(t, "three\nfour", tail)

	all, err := tailFile(path, -1)
	require.NoError(t, err)
	assert.Equal(t, "one\ntwo\nthree\nfour", all)

	bigger, err := tailFile(path, 100)
	require.NoError(t, err)
	assert.Equal(t, "one\ntwo\nthree\nfour", bigger)

	require.NoError(t, os.WriteFile(path, []byte(""), 0600))
	empty, err := tailFile(path, 10)
	require.NoError(t, err)
	assert.Equal(t, "", empty)
}
