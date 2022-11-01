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

package slurm_test

import (
	"strings"
	"testing"
	"text/template"

	"github.com/carv-ics-forth/hpk/compute/slurm"
	"github.com/pkg/errors"
)

// Run with: go clean -testcache && go test ./ -v  -run TestApptainer
func TestApptainer(t *testing.T) {
	tests := []struct {
		name     string
		fields   slurm.ApptainerTemplateFields
		expected string
	}{
		{
			name: "noenv",
			fields: slurm.ApptainerTemplateFields{
				Image:   "docker://alpine:3.7",
				Command: []string{"echo", "pizza"},
				Args:    []string{"hello", "world"},
			},
			expected: `
apptainer exec \
docker://alpine:3.7 echo pizza hello world
`,
		},
		{
			name: "env",
			fields: slurm.ApptainerTemplateFields{
				Image:   "docker://alpine:3.7",
				Command: []string{"echo", "pizza"},
				Args:    []string{"hello", "world"},
				Env:     []string{"VAR=val1", "VAR2=val2"},
			},
			expected: `
apptainer exec \
--env VAR=val1,VAR2=val2 \
docker://alpine:3.7 echo pizza hello world
`,
		},
		{
			name: "bind",
			fields: slurm.ApptainerTemplateFields{
				Image:   "docker://alpine:3.7",
				Command: []string{"echo", "pizza"},
				Args:    []string{"hello", "world"},
				Env:     []string{"VAR=val1", "VAR2=val2"},
				Bind:    []string{"/hostpath1:/containerpath1", "/hostpath2", "/hostpath3:/containerpath3"},
			},
			expected: `
apptainer exec \
--env VAR=val1,VAR2=val2 \
--bind /hostpath1:/containerpath1,/hostpath2,/hostpath3:/containerpath3 \
docker://alpine:3.7 echo pizza hello world
`,
		},
	}

	submitTpl, err := template.New("test").Option("missingkey=error").Parse(slurm.ApptainerTemplate)
	if err != nil {
		t.Fatal(err)
	}

	for _, tt := range tests {
		var apptainerCmd strings.Builder

		if err := submitTpl.Execute(&apptainerCmd, tt.fields); err != nil {
			/*-- since both the template and fields are internal to the code, the evaluation should always succeed	--*/
			panic(errors.Wrapf(err, "failed to evaluate apptainer cmd"))
		}

		actual := apptainerCmd.String() // strings.TrimSpace(apptainerCmd.String())
		expected := tt.expected         // strings.TrimSpace(tt.expected)

		if strings.Compare(expected, actual) != 0 {
			t.Fatalf("Expected '%s' but got '%s'", expected, actual)
		}
	}
}
