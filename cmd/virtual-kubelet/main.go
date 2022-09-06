/*
Copyright 2022 ICS-FORTH.
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"context"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/carv-ics-forth/knoc/api"
	"github.com/carv-ics-forth/knoc/cmd/virtual-kubelet/commands/providers"
	"github.com/carv-ics-forth/knoc/cmd/virtual-kubelet/commands/root"
	"github.com/carv-ics-forth/knoc/cmd/virtual-kubelet/commands/version"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/virtual-kubelet/virtual-kubelet/log"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sig
		cancel()
	}()

	var opts root.Opts

	rootCmd := root.NewCommand(ctx, filepath.Base(os.Args[0]), opts)
	rootCmd.AddCommand(version.NewCommand(api.BuildVersion, api.BuildTime), providers.NewCommand())
	preRun := rootCmd.PreRunE

	var logLevel string
	rootCmd.PreRunE = func(cmd *cobra.Command, args []string) error {
		if preRun != nil {
			return preRun(cmd, args)
		}
		return nil
	}

	rootCmd.PersistentFlags().StringVar(&logLevel, "log-level", "info", `set the log level, e.g. "debug", "info", "warn", "error"`)

	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		if logLevel != "" {
			lvl, err := logrus.ParseLevel(logLevel)
			if err != nil {
				return errors.Wrap(err, "could not parse log level")
			}
			logrus.SetLevel(lvl)
		}
		return nil
	}

	if err := rootCmd.Execute(); err != nil && errors.Cause(err) != context.Canceled {
		log.G(ctx).Fatal(err)
	}
}
