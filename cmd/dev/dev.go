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

// Package dev implements the 'okteto dev' command group: a non-interactive way to run
// the same development environment as 'okteto up' (code sync + port-forwards + app
// command), designed to be driven by automation and coding agents. Unlike 'okteto up',
// these commands never take over the terminal and always return control to the caller.
package dev

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	contextCMD "github.com/okteto/okteto/cmd/context"
	"github.com/okteto/okteto/cmd/utils"
	"github.com/okteto/okteto/pkg/config"
	"github.com/okteto/okteto/pkg/discovery"
	oktetoErrors "github.com/okteto/okteto/pkg/errors"
	oktetoLog "github.com/okteto/okteto/pkg/log"
	"github.com/okteto/okteto/pkg/model"
	"github.com/okteto/okteto/pkg/okteto"
	"github.com/okteto/okteto/pkg/validator"
	"github.com/spf13/afero"
	"github.com/spf13/cobra"
)

// commonFlags are the flags shared by all 'okteto dev' subcommands
type commonFlags struct {
	manifestPath string
	namespace    string
	k8sContext   string
}

func (f *commonFlags) register(cmd *cobra.Command) {
	cmd.Flags().StringVarP(&f.manifestPath, "file", "f", "", "the path to the Okteto Manifest")
	cmd.Flags().StringVarP(&f.namespace, "namespace", "n", "", "overwrite the current Okteto Namespace")
	cmd.Flags().StringVarP(&f.k8sContext, "context", "c", "", "overwrite the current Okteto Context")
}

// Dev groups the commands to manage non-interactive development sessions
func Dev(fs afero.Fs) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dev",
		Short: "Manage non-interactive development sessions (automation friendly)",
		Long: `Manage non-interactive development sessions.

'okteto dev' runs the same development environment as 'okteto up' (code sync,
port-forwards and your app command) but detached: commands return control
immediately and report status in a machine-readable way. 'okteto up' is
untouched; 'okteto dev' sits alongside it for automation and coding agents.`,
		Example: `# Start a development session for the 'api' dev container and return when it is ready
okteto dev start api

# Check if the session is ready (exit codes: 0 ready, 1 failed, 3 not ready yet, 4 no session)
okteto dev status api

# Stream the app output
okteto dev logs api

# Restart the session (e.g. to re-run the app command)
okteto dev restart api

# Tear the session down and deactivate the Development Container
okteto dev stop api`,
	}
	cmd.AddCommand(Start(fs))
	cmd.AddCommand(Status(fs))
	cmd.AddCommand(Logs(fs))
	cmd.AddCommand(Restart(fs))
	cmd.AddCommand(Stop(fs))
	return cmd
}

// devEnvironment is the resolved target of a 'okteto dev' subcommand
type devEnvironment struct {
	manifest  *model.Manifest
	dev       *model.Dev
	name      string
	namespace string
}

// resolveDevEnvironment initializes the okteto context, loads the manifest and resolves
// the dev container to operate on. It never prompts: with more than one dev container
// in the manifest, the name must be passed as an argument.
func resolveDevEnvironment(ctx context.Context, fs afero.Fs, flags *commonFlags, args []string, showContext bool) (*devEnvironment, error) {
	if err := validator.FileArgumentIsNotDir(fs, flags.manifestPath); err != nil {
		return nil, err
	}

	if okteto.InDevContainer() {
		return nil, oktetoErrors.ErrNotInDevContainer
	}

	ctxOpts := &contextCMD.Options{
		Show:      showContext,
		Context:   flags.k8sContext,
		Namespace: flags.namespace,
	}
	if err := contextCMD.NewContextCommand().Run(ctx, ctxOpts); err != nil {
		return nil, err
	}

	manifest, err := model.GetManifestV2(flags.manifestPath, fs)
	if err != nil {
		if errors.Is(err, discovery.ErrOktetoManifestNotFound) {
			return nil, oktetoErrors.UserError{
				E:    fmt.Errorf("okteto manifest not found"),
				Hint: "Run 'okteto dev' from the folder that contains your okteto manifest or use the '-f' flag to point to it",
			}
		}
		return nil, err
	}

	if !okteto.IsOkteto() {
		if err := manifest.ValidateForCLIOnly(); err != nil {
			return nil, err
		}
	}

	if len(manifest.Dev) == 0 {
		return nil, oktetoErrors.ErrManifestNoDevSection
	}

	devName := ""
	if len(args) > 0 {
		devName = args[0]
	}
	if devName == "" {
		devNames := manifest.Dev.GetDevs()
		if len(devNames) == 1 {
			devName = devNames[0]
		} else {
			return nil, oktetoErrors.UserError{
				E:    fmt.Errorf("there are multiple dev containers defined in your okteto manifest"),
				Hint: fmt.Sprintf("Specify one: %s", strings.Join(devNames, ", ")),
			}
		}
	}

	dev, err := utils.GetDevFromManifest(manifest, devName)
	if err != nil {
		return nil, err
	}

	return &devEnvironment{
		manifest:  manifest,
		dev:       dev,
		name:      devName,
		namespace: okteto.GetContext().Namespace,
	}, nil
}

// appLogPath returns the path to the 'okteto up' debug log of the dev environment
func appLogPath(namespace, devName string) string {
	return filepath.Join(config.GetAppHome(namespace, devName), "okteto.log")
}

// resolveSessionNamespace finds the namespace whose session the command should operate
// on. Sessions are keyed by the namespace they were started in, which may differ from
// the current context namespace (e.g. after 'okteto namespace use'), so when the current
// namespace has no session the recorded sessions are the source of truth. An explicit
// '--namespace' flag always wins and disables the search.
func resolveSessionNamespace(env *devEnvironment, explicitNamespace string) (string, error) {
	if explicitNamespace != "" {
		return env.namespace, nil
	}
	if hasSessionFiles(env.namespace, env.name) {
		return env.namespace, nil
	}

	sessions := findSessions(env.name)
	switch len(sessions) {
	case 0:
		return env.namespace, nil
	case 1:
		if sessions[0].Namespace != env.namespace {
			oktetoLog.Information("Using the development session for '%s' started in namespace '%s'", env.name, sessions[0].Namespace)
		}
		return sessions[0].Namespace, nil
	default:
		namespaces := make([]string, len(sessions))
		for i, s := range sessions {
			namespaces[i] = s.Namespace
		}
		return "", oktetoErrors.UserError{
			E:    fmt.Errorf("there are development sessions for '%s' in multiple namespaces: %s", env.name, strings.Join(namespaces, ", ")),
			Hint: fmt.Sprintf("Specify one with the '--namespace' flag, e.g. okteto dev stop %s -n %s", env.name, namespaces[0]),
		}
	}
}
