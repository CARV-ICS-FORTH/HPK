// Copyright © 2023 FORTH-ICS
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

package image

import (
	"github.com/carv-ics-forth/hpk/compute"
	"github.com/carv-ics-forth/hpk/pkg/process"
)

// Image is an actionable object of a container image.
type Image struct {
	// Filepath points to the location where the image is stored.
	Filepath string
}

// FakerootExec uses Singularity to instantiate the image and run a command.
func (p *Image) FakerootExec(singularityArgs []string, cmd []string) (string, error) {
	execCmd := []string{"exec", "--fakeroot"}
	execCmd = append(execCmd, singularityArgs...)
	execCmd = append(execCmd, p.Filepath)
	execCmd = append(execCmd, cmd...)

	out, err := process.Execute(compute.Environment.ApptainerBin, execCmd...)

	return string(out), err
}
