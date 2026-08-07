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
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildUpArgs(t *testing.T) {
	tests := []struct {
		name     string
		flags    *startFlags
		command  []string
		expected []string
	}{
		{
			name:     "defaults",
			flags:    &startFlags{},
			expected: []string{"up", "api", "--log-output", "plain"},
		},
		{
			name: "all flags",
			flags: &startFlags{
				commonFlags: commonFlags{
					manifestPath: "okteto.yml",
					namespace:    "ns",
					k8sContext:   "ctx",
				},
				deploy: true,
				reset:  true,
				envs:   []string{"FOO=bar", "BAZ=qux"},
			},
			expected: []string{
				"up", "api", "--log-output", "plain",
				"--file", "okteto.yml",
				"--namespace", "ns",
				"--context", "ctx",
				"--deploy",
				"--reset",
				"--env", "FOO=bar",
				"--env", "BAZ=qux",
			},
		},
		{
			name:     "command passthrough",
			flags:    &startFlags{},
			command:  []string{"yarn", "start"},
			expected: []string{"up", "api", "--log-output", "plain", "--", "yarn", "start"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, buildUpArgs("api", tt.flags, tt.command))
		})
	}
}

func TestStartPhasesSummary(t *testing.T) {
	base := time.Date(2026, 8, 7, 13, 40, 0, 0, time.UTC)
	tests := []struct {
		name     string
		phases   startPhases
		expected string
	}{
		{
			name: "all phases observed",
			phases: startPhases{
				start:      base,
				activating: base.Add(35 * time.Second),
				syncing:    base.Add(75 * time.Second),
				ready:      base.Add(82 * time.Second),
			},
			expected: "1m22s (deploy 35s, container 40s, sync 7s)",
		},
		{
			name: "sync phase not observed",
			phases: startPhases{
				start:      base,
				activating: base.Add(5 * time.Second),
				ready:      base.Add(20 * time.Second),
			},
			expected: "20s (deploy 5s, container 15s)",
		},
		{
			name: "no phases observed",
			phases: startPhases{
				start: base,
				ready: base.Add(3 * time.Second),
			},
			expected: "3s",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.phases.summary())
		})
	}
}

func TestLastLogLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "log")

	require.NoError(t, os.WriteFile(path, []byte("first\nExecuting command 'Deploy Rent'...\n"), 0600))
	assert.Equal(t, "Executing command 'Deploy Rent'...", lastLogLine(path))

	// ANSI escapes and control characters from shell prompts are stripped
	require.NoError(t, os.WriteFile(path, []byte("\x1b[?2004h\x1b[36mrberrelleza:\x1b[32mapi \x1b[mapp>\n"), 0600))
	assert.Equal(t, "rberrelleza:api app>", lastLogLine(path))

	// long lines are truncated
	require.NoError(t, os.WriteFile(path, []byte(strings.Repeat("a", 300)+"\n"), 0600))
	line := lastLogLine(path)
	assert.Len(t, line, maxHeartbeatLineLength+3)
	assert.True(t, strings.HasSuffix(line, "..."))

	// missing file is not an error
	assert.Equal(t, "", lastLogLine(filepath.Join(dir, "missing")))
}

func TestPositionalArgs(t *testing.T) {
	tests := []struct {
		name          string
		args          []string
		argsLenAtDash int
		expected      []string
	}{
		{name: "no dash", args: []string{"api"}, argsLenAtDash: -1, expected: []string{"api"}},
		{name: "dash with dev", args: []string{"api", "yarn", "start"}, argsLenAtDash: 1, expected: []string{"api"}},
		{name: "dash without dev", args: []string{"yarn", "start"}, argsLenAtDash: 0, expected: []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, positionalArgs(tt.args, tt.argsLenAtDash))
		})
	}
}
