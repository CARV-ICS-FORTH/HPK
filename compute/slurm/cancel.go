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

// Package job contains code for accessing compute resources via Slurm.
package slurm

import (
	"strings"

	"github.com/carv-ics-forth/hpk/pkg/process"
	"github.com/pkg/errors"
)

var Signal = "--signal=TERM"

// SignalChildren signals the submission script and the pause environment.
// SignalChildren signals the batch script and its children processes (pause).
var SignalChildren = "--full"

// SignalParentOnly signals only the submission script.
// https://slurm.schedmd.com/scancel.html#OPT_batch
var SignalParentOnly = "--batch"

func CancelJob(args string) (string, error) {
	/*
	 Install trap for the signals INT and TERM to
	 the main BATCH script here.
	 Send SIGTERM using kill to the internal script's
	 process and wait for it to close gracefully.
	*/
	out, err := process.Execute(Slurm.CancelCmd, Signal, SignalChildren, args)
	if err != nil {
		// in this case, the job does not exist, so for what it matters it is terminated.
		if strings.Contains(string(out), "Invalid job id specified") {
			return "", nil
		}

		return string(out), errors.Wrap(err, "Could not run scancel")
	}

	return string(out), nil
}
