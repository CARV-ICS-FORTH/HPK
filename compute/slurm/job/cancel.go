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
	"github.com/carv-ics-forth/hpk/pkg/process"
	"github.com/pkg/errors"
)

func CancelJob(args string) (string, error) {
	out, err := process.Execute(Slurm.CancelCmd, args)
	if err != nil {
		return string(out), errors.Wrap(err, "Could not run scancel")
	}

	return string(out), nil
}
