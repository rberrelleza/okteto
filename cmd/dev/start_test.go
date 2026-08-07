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
