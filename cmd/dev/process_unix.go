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

//go:build !windows
// +build !windows

package dev

import (
	"os/exec"
	"syscall"
)

// configureDetachedProcess makes the child run in its own session so it keeps
// running after 'okteto dev start' returns and isn't tied to the caller's terminal
func configureDetachedProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true,
	}
}

// terminateProcess asks the process to shut down cleanly. 'okteto up' handles it
// by running its shutdown sequence.
func terminateProcess(pid int) error {
	return syscall.Kill(pid, syscall.SIGTERM)
}

// killProcess kills the process without waiting for a clean shutdown
func killProcess(pid int) error {
	return syscall.Kill(pid, syscall.SIGKILL)
}
