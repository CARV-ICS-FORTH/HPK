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

package slurm

/************************************************************

		Apptainer Execution Templates

************************************************************/

var ApptainerTemplate = `
apptainer exec \
{{- if .Env}}
--env {{ range $index, $variable := .Env}}{{if $index}},{{end}}{{$variable}}{{end}} \
{{- end}}
{{- if .Bind}}
--bind {{ range $index, $path := .Bind}}{{if $index}},{{end}}{{$path}}{{end}} \
{{- end}}
{{.Image}} 
{{- if .Command}} {{ range $index, $cmd := .Command}}{{if $index}} {{end}}{{$cmd}}{{end}}{{- end}}
{{- if .Args}} {{ range $index, $arg := .Args}}{{if $index}} {{end}}{{$arg}}{{end}}{{- end}}
`

/*

var ApptainerTemplate = `
apptainer exec \
{{- if .Env}}
--env {{ range $index, $variable := .Env}}{{if $index}},{{end}}{{$variable}}{{end}} \
{{- end}}
{{- if .Bind}}
--bind {{ range $index, $path := .Bind}}{{if $index}},{{end}}{{$path}}{{end}} \
{{- end}}
{{.Image}}
{{- if .Command}} {{ range $index, $cmd := .Command}}{{if $index}} {{end}}{{$cmd}}{{end}}{{- end}}
{{- if .Args}} {{ range $index, $arg := .Args}}{{if $index}} {{end}}{{$arg}}{{end}}{{- end}}
`

*/

// ApptainerTemplateFields container the supported fields for the Apptainer template.
type ApptainerTemplateFields struct {
	/*--
		Mandatory Fields
	--*/
	Image   string // format: REGISTRY://image:tag
	Command []string
	Args    []string // space separated args

	/*--
		Optional Fields (marked by a pointer)
	--*/
	Env  []string // format: VAR=VALUE
	Bind []string // format: /hostpath:/containerpath or /hostpath
}

/************************************************************

			Sbatch Templates

************************************************************/

// SBatchTemplate provides the context for going from Apptainer jobs to slurm jobs
// Single # are directives to SBATCH
// Double ## are comments.
var SBatchTemplate = `#!/bin/bash
#SBATCH --job-name={{.JobName}}
#SBATCH --output={{.StdLogsPath}}.stdout
#SBATCH --error={{.StdLogsPath}}.stderr

{{- if .NTasksPerNode}}
#SBATCH --ntasks-per-node={{.NTasksPerNode}}
{{end}}

{{- if .CPUPerTask}}
#SBATCH --cpus-per-task={{.CPUPerTask}}  # usually, obviously, in the range[1-10]
{{end}}

{{- if .Nodes}}
#SBATCH --nodes={{.Nodes}}
{{end}}

{{- if .MemoryPerNode}}
#SBATCH --mem={{.MemoryPerNode}} # e.g 400GB
{{end}}

{{- if .DependencyList}}
#SBATCH --dependency afterok:{{.DependencyList}}
{{end}}

{{- if .CustomFlags}}
{{.CustomFlags}}
{{end}}

set -eum pipeline

trap cleanup EXIT
cleanup() {
   exit_code=$?
   echo $exit_code > {{.ExitCodePath}}
}

pwd; hostname; date

echo "Starting Apptainer Job"
{{.ApptainerCommand}}
`

// SBatchTemplateFields container the supported fields for the submission template.
type SBatchTemplateFields struct {
	/*--
		Mandatory Fields
	--*/

	// JobName indicate the name of the sbatch job
	JobName string

	// ApptainerCommand is the evaluated Apptainer command to be executed within sbatch.
	ApptainerCommand string

	// StdLogsPath instruct Slurm to write stdout and stderr into the specified path
	StdLogsPath string

	// ExitCodePath is the path where the embedded Apptainer command will write its exit code
	ExitCodePath string

	/*--
		Optional Fields (marked by a pointer)
	--*/

	// Nodes request that a minimum of number nodes are allocated to this job.
	Nodes *int

	// NTasksPerNode request that ntasks be invoked on each node
	NTasksPerNode *int

	// CPUPerTask advise the Slurm controller that ensuing job steps will require ncpus number of processors per task.
	CPUPerTask *int

	// MemoryPerNode Specify the real memory required per node.
	MemoryPerNode *string

	// DependencyList defer the start of this job until the specified dependencies have been satisfied completed.
	DependencyList []string

	// CustomFlags are sbatch that are directly given by the end-user.
	CustomFlags []string
}
