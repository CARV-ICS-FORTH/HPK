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
	"fmt"
	"strings"
	"testing"
	"text/template"

	"github.com/Masterminds/sprig"
	"github.com/carv-ics-forth/hpk/compute"
	"github.com/carv-ics-forth/hpk/compute/slurm"
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
				Image:     "docker://alpine:3.7",
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
				Image:       "docker://alpine:3.7",
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
				Image:       "docker://alpine:3.7",
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
			panic(errors.Wrapf(err, "failed to evaluate apptainer cmd"))
		}

		actual := apptainerCmd.String() // strings.TrimSpace(apptainerCmd.String())
		expected := tt.expected         // strings.TrimSpace(tt.expected)

		if strings.Compare(expected, actual) != 0 {
			t.Fatalf("Test[%s], Expected :\n'%s' but got: \n'%s'", tt.name, expected, actual)
		}
	}
}
*/

// Run with: go clean -testcache && go test ./ -v  -run TestSBatch
func TestSBatch(t *testing.T) {
	podKey := types.NamespacedName{
		Namespace: "dummy",
		Name:      "dummier",
	}

	podDir := compute.PodRuntimeDir(podKey)

	tests := []struct {
		name   string
		fields slurm.SbatchScriptFields
	}{
		{
			name: "noenv",
			fields: slurm.SbatchScriptFields{
				ComputeEnv: compute.HPCEnvironment{
					KubeServiceHost:   "6.6.6.6",
					KubeServicePort:   "666",
					KubeDNS:           "6.6.6.6",
					ContainerRegistry: "none",
				},
				Pod: podKey,
				VirtualEnv: slurm.VirtualEnvironmentPaths{
					ConstructorPath: podDir.ConstructorPath(),
					IPAddressPath:   podDir.IPAddressPath(),
					JobIDPath:       podDir.IDPath(),
					StdoutPath:      podDir.StdoutPath(),
					StderrPath:      podDir.StderrPath(),
					ExitCodePath:    podDir.ExitCodePath(),
				},
				Options: slurm.RequestOptions{},
				InitContainers: func() (containers []slurm.Container) {
					containerName := []string{"a", "b", "c", "d"}
					delays := []string{"5", "10", "8", "5"}

					for i, cname := range containerName {
						containers = append(containers, slurm.Container{
							Command:      []string{"sleep " + delays[i], "echo " + cname},
							JobIDPath:    podDir.Container(cname).IDPath(),
							LogsPath:     podDir.Container(cname).LogsPath(),
							ExitCodePath: podDir.Container(cname).ExitCodePath(),
						})
					}

					return containers
				}(),

				Containers: func() (containers []slurm.Container) {
					containerName := []string{"server", "client"}
					args := []string{"-s", "-c iperf-server \n \n"}

					for i, cname := range containerName {
						containers = append(containers, slurm.Container{
							Command:      []string{"# Some random comment ", "\n", "sleep 10; " + args[i]},
							JobIDPath:    podDir.Container(cname).IDPath(),
							LogsPath:     podDir.Container(cname).LogsPath(),
							ExitCodePath: podDir.Container(cname).ExitCodePath(),
						})
					}

					return containers
				}(),
			},
		},
	}

	submitTpl, err := template.New("test").
		Funcs(sprig.FuncMap()).
		Option("missingkey=error").
		Parse(slurm.SbatchScriptTemplate)
	if err != nil {
		t.Fatal(err)
	}

	for _, tt := range tests {
		var sbatchScript strings.Builder

		if err := submitTpl.Execute(&sbatchScript, tt.fields); err != nil {
			/*-- since both the template and fields are internal to the code, the evaluation should always succeed	--*/
			panic(errors.Wrapf(err, "failed to evaluate sbatch"))
		}

		actual := sbatchScript.String() // strings.TrimSpace(sbatchScript.String())

		fmt.Println(actual)
	}
}
