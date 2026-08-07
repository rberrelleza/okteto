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
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuildRestartArgs(t *testing.T) {
	tests := []struct {
		name         string
		prev         *session
		flags        *restartFlags
		expectedArgs []string
		expectedDir  string
	}{
		{
			name:         "no previous session builds fresh args",
			prev:         nil,
			flags:        &restartFlags{},
			expectedArgs: []string{"up", "api", "--log-output", "plain"},
		},
		{
			name: "recorded args are reused and pinned to the session namespace and context",
			prev: &session{
				Namespace: "rberrelleza",
				Context:   "https://demo.okteto.dev",
				Dir:       "/Users/ramiro/okteto/movies",
				Args:      []string{"up", "api", "--log-output", "plain"},
			},
			flags: &restartFlags{},
			expectedArgs: []string{
				"up", "api", "--log-output", "plain",
				"--namespace", "rberrelleza",
				"--context", "https://demo.okteto.dev",
			},
			expectedDir: "/Users/ramiro/okteto/movies",
		},
		{
			name: "recorded namespace flag is not duplicated",
			prev: &session{
				Namespace: "rberrelleza",
				Args:      []string{"up", "api", "--log-output", "plain", "--namespace", "rberrelleza"},
			},
			flags: &restartFlags{},
			expectedArgs: []string{
				"up", "api", "--log-output", "plain", "--namespace", "rberrelleza",
			},
		},
		{
			name: "pinned flags stay before the command separator",
			prev: &session{
				Namespace: "rberrelleza",
				Args:      []string{"up", "api", "--log-output", "plain", "--", "yarn", "start"},
			},
			flags: &restartFlags{},
			expectedArgs: []string{
				"up", "api", "--log-output", "plain",
				"--namespace", "rberrelleza",
				"--", "yarn", "start",
			},
		},
		{
			name: "reset flag is added on demand",
			prev: &session{
				Namespace: "rberrelleza",
				Args:      []string{"up", "api", "--log-output", "plain", "--namespace", "rberrelleza"},
			},
			flags: &restartFlags{reset: true},
			expectedArgs: []string{
				"up", "api", "--log-output", "plain", "--namespace", "rberrelleza", "--reset",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args, dir := buildRestartArgs("api", tt.prev, tt.flags)
			assert.Equal(t, tt.expectedArgs, args)
			assert.Equal(t, tt.expectedDir, dir)
		})
	}
}

func TestUpFlagPresent(t *testing.T) {
	args := []string{"up", "api", "--namespace", "ns", "-e", "FOO=bar", "--", "-n", "fake"}
	assert.True(t, upFlagPresent(args, "--namespace", "-n"))
	assert.True(t, upFlagPresent(args, "-e"))
	assert.False(t, upFlagPresent(args, "--context", "-c"))
	// flags after the command separator belong to the command, not to 'okteto up'
	assert.False(t, upFlagPresent([]string{"up", "api", "--", "-n"}, "-n"))
	assert.True(t, upFlagPresent([]string{"up", "--namespace=ns"}, "--namespace"))
}

func TestInsertUpArgs(t *testing.T) {
	original := []string{"up", "api", "--", "yarn", "start"}
	result := insertUpArgs(original, "--reset")
	assert.Equal(t, []string{"up", "api", "--reset", "--", "yarn", "start"}, result)
	// the input slice is not modified
	assert.Equal(t, []string{"up", "api", "--", "yarn", "start"}, original)

	assert.Equal(t, []string{"up", "api", "--reset"}, insertUpArgs([]string{"up", "api"}, "--reset"))
	assert.Equal(t, []string{"up", "api"}, insertUpArgs([]string{"up", "api"}))
}

func TestHasCommandOverride(t *testing.T) {
	assert.True(t, hasCommandOverride([]string{"up", "api", "--", "yarn", "start"}))
	assert.False(t, hasCommandOverride([]string{"up", "api", "--reset"}))
}
