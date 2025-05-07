// Copyright © 2022 FORTH-ICS
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/carv-ics-forth/hpk/cmd/hpk/commands"
	"github.com/carv-ics-forth/hpk/cmd/hpk/commands/root"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/virtual-kubelet/virtual-kubelet/log"
)

// NewRootCommand creates a new providers subcommand
// This subcommand is used to determine which providers are registered.
func NewRootCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "providers",
		Short: "Show the list of supported providers",
		Long:  "Show the list of supported providers",
		Args:  cobra.MaximumNArgs(2),
		Run: func(cmd *cobra.Command, args []string) {
			switch len(args) {
			case 0:
				fmt.Fprintln(cmd.OutOrStdout(), "hpk")
			case 1:
				if args[0] != "hpk" {
					fmt.Fprintln(cmd.OutOrStderr(), "no such provider", args[0])

					os.Exit(1)
				}
				fmt.Fprintln(cmd.OutOrStdout(), args[0])
			}
			return
		},
	}
}

// NewVersionCommand creates a new version subcommand command
func NewVersionCommand(version, buildTime string) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Show the version of the program",
		Long:  `Show the version of the program`,
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("Version: %s, Built: %s\n", version, buildTime)
		},
	}
}

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
	rootCmd.AddCommand(NewVersionCommand(commands.BuildVersion, commands.BuildTime), NewRootCommand())
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
