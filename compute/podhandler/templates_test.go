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

package podhandler_test

import (
	"log"
	"os"
	"strings"
	"testing"

	"github.com/carv-ics-forth/hpk/compute"
	PodHandler "github.com/carv-ics-forth/hpk/compute/podhandler"
	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/types"
)

// Run with: go clean -testcache && go test ./ -v  -run TestApptainer
/*
func TestApptainer(t *testing.T) {
	tests := []struct {
		name     string
		fields   slurm.ApptainerTemplateFields
		expected string
	}{
		{
			name: "noenv",
			fields: slurm.ApptainerTemplateFields{
				Apptainer: slurm.ApptainerExec,
				ImageFilePath:     "docker://alpine:3.7",
				Command:   []string{"sh", "-c"},
				Args:      []string{"echo", "hello world"},
			},
			expected: slurm.ApptainerExec + `
docker://alpine:3.7 "sh" "-c" "echo" "hello world"`,
		},
		{
			name: "env",
			fields: slurm.ApptainerTemplateFields{
				Apptainer:   slurm.ApptainerExec,
				ImageFilePath:       "docker://alpine:3.7",
				Command:     []string{"sh", "-c"},
				Args:        []string{"echo", "hello world"},
				Environment: []string{"VAR=val1", "VAR2=val2"},
			},
			expected: slurm.ApptainerExec + `
--env VAR=val1,VAR2=val2 \
docker://alpine:3.7 "sh" "-c" "echo" "hello world"`,
		},
		{
			name: "bind",
			fields: slurm.ApptainerTemplateFields{
				Apptainer:   slurm.ApptainerExec,
				ImageFilePath:       "docker://alpine:3.7",
				Command:     []string{"sh", "-c"},
				Args:        []string{"echo", "hello world"},
				Environment: []string{"VAR=val1", "VAR2=val2"},
				Bind:        []string{"/hostpath1:/containerpath1", "/hostpath2", "/hostpath3:/containerpath3"},
			},
			expected: slurm.ApptainerExec + `
--env VAR=val1,VAR2=val2 \
--bind /hostpath1:/containerpath1,/hostpath2,/hostpath3:/containerpath3 \
docker://alpine:3.7 "sh" "-c" "echo" "hello world"`,
		},
	}

	submitTpl, err := template.New("test").
		Funcs(sprig.FuncMap()).
		Option("missingkey=error").
		Parse(slurm.ApptainerTemplate)
	if err != nil {
		t.Fatal(err)
	}

	for _, tt := range tests {
		var apptainerCmd strings.Builder

		if err := submitTpl.Execute(&apptainerCmd, tt.fields); err != nil {
			/*-- since both the template and fields are internal to the code, the evaluation should always succeed	--* /
			panic(errors.Wrapf(err, "failed to evaluate Apptainer cmd"))
		}

		actual := apptainerCmd.String() // strings.TrimSpace(apptainerCmd.String())
		expected := tt.expected         // strings.TrimSpace(tt.expected)

		if strings.Compare(expected, actual) != 0 {
			t.Fatalf("Test[%s], Expected :\n'%s' but got: \n'%s'", tt.name, expected, actual)
		}
	}
}
*/

// Run with: go clean -testcache && go test ./ -v  -run TestScriptSyntax
func TestConstructorSyntax(t *testing.T) {
	podKey := types.NamespacedName{
		Namespace: "dummy",
		Name:      "dummier",
	}

	podDir := compute.HPK.Pod(podKey)

	tests := []struct {
		name   string
		fields PodHandler.JobFields
	}{
		{
			name: "noenv",
			fields: PodHandler.JobFields{
				HostEnv: compute.HostEnvironment{
					ApptainerBin:      "apptainer",
					KubeDNS:           "6.6.6.6",
					ContainerRegistry: "none",
				},
				Pod: podKey,
				VirtualEnv: compute.VirtualEnvironment{
					PodDirectory:        podDir.String(),
					CgroupFilePath:      podDir.CgroupFilePath(),
					ConstructorFilePath: podDir.ConstructorFilePath(),
					IPAddressPath:       podDir.IPAddressPath(),
					StdoutPath:          podDir.StdoutPath(),
					StderrPath:          podDir.StderrPath(),
					SysErrorFilePath:    "",
				},
				InitContainers: []PodHandler.Container{
					{
						InstanceName:  "init0",
						RunAsUser:     0,
						RunAsGroup:    0,
						ImageFilePath: "/image/path",
						EnvFilePath:   "/env/path",
						Binds:         nil,
						Command:       []string{"ls"},
						Args:          []string{"-lah"},
						ExecutionMode: "run",
						LogsPath:      podDir.Container("init0").LogsPath(),
						JobIDPath:     podDir.Container("init0").IDPath(),
						ExitCodePath:  podDir.Container("init0").ExitCodePath(),
					},
					{
						InstanceName:  "init1",
						RunAsUser:     0,
						RunAsGroup:    0,
						ImageFilePath: "/image/path",
						EnvFilePath:   "/env/path",
						Binds:         nil,
						Command:       []string{"touch"},
						Args:          []string{"miax"},
						ExecutionMode: "run",
						LogsPath:      podDir.Container("init1").LogsPath(),
						JobIDPath:     podDir.Container("init1").IDPath(),
						ExitCodePath:  podDir.Container("init1").ExitCodePath(),
					},
				},

				Containers: []PodHandler.Container{
					{
						InstanceName:  "lala",
						RunAsUser:     0,
						RunAsGroup:    0,
						ImageFilePath: "/image/path",
						EnvFilePath:   "/env/path",
						Binds:         nil,
						Command: []string{`
                          # Peculiar expressions that cause issues
                          cut -d ' ' -f 4 /proc/self/stat >

						  cat << EOF > /tmp/test.py	
						  with open('/tmp/somepath.json', 'w') as f:
						  EOF
						`},
						Args:          []string{"some additional", "args"},
						ExecutionMode: "run",
						LogsPath:      podDir.Container("containerA").LogsPath(),
						JobIDPath:     podDir.Container("containerA").IDPath(),
						ExitCodePath:  podDir.Container("containerA").ExitCodePath(),
					},
					{
						InstanceName:  "sidecar",
						RunAsUser:     0,
						RunAsGroup:    0,
						ImageFilePath: "/image/path",
						EnvFilePath:   "/env/path",
						Binds:         nil,
						// Stupid unescaped args
						Command: []string{`
							Try some terminated quotes: "", '', "''",
							Try some unterminated quotes: ', ", \', \",
						`},
						ExecutionMode: "run",
						LogsPath:      podDir.Container("containerA").LogsPath(),
						JobIDPath:     podDir.Container("containerA").IDPath(),
						ExitCodePath:  podDir.Container("containerA").ExitCodePath(),
					},
				},
			},
		},
	}

	submitTpl, err := PodHandler.ParseTemplate(PodHandler.PauseScriptTemplate)
	if err != nil {
		t.Fatal(err)
	}

	for _, tt := range tests {
		var sbatchScript strings.Builder

		if err := submitTpl.Execute(&sbatchScript, tt.fields); err != nil {
			/*-- since both the template and fields are internal to the code, the evaluation should always succeed	--*/
			panic(errors.Wrapf(err, "failed to evaluate sbatch"))
		}

		/*---------------------------------------------------
		 * Validate the syntax of the generated script
		 *---------------------------------------------------*/
		// populate a temporary file with the generated content
		f, err := os.CreateTemp("", "constructor.*.sh")
		if err != nil {
			log.Fatal(err)
		}

		_, err = f.WriteString(sbatchScript.String())
		if err != nil {
			log.Fatal(err)
		}

		if err := PodHandler.ValidateScript(f.Name()); err != nil {
			log.Fatal(err)
		}

		t.Log("Tmpfile", f.Name())
		// os.Remove(f.Name())
	}
}
