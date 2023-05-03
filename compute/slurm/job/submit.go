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
package job

import (
	"regexp"
	"strconv"

	"github.com/carv-ics-forth/hpk/compute"
	"github.com/carv-ics-forth/hpk/pkg/process"
)

// ExcludeNodes EXISTS ONLY FOR DEBUGGING PURPOSES of Inotify on NFS.
var ExcludeNodes = "--exclude="

// NewUserEnv is used to generate the /run/user/ folder required by cgroups.
var NewUserEnv = "--get-user-env"

func SubmitJob(scriptFile string) (string, error) {
	// Submit Job
	out, err := process.ExecuteInDir(compute.UserHomeDir, Slurm.SubmitCmd, ExcludeNodes, NewUserEnv, scriptFile)
	if err != nil {
		compute.SystemPanic(err, "job submission error. out : '%s'", out)
	}

	// Parse Job ID
	expectedOutput := regexp.MustCompile(`Submitted batch job (?P<jid>\d+)`)
	jid := expectedOutput.FindStringSubmatch(string(out))

	if _, err := strconv.Atoi(jid[1]); err != nil {
		compute.SystemPanic(err, "Invalid JobID")
	}

	return jid[1], nil
}
