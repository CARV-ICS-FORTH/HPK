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

import (
	"k8s.io/apimachinery/pkg/types"
)

/************************************************************

		Apptainer Execution Templates

************************************************************/

const (
	ApptainerWithoutCommand = "apptainer run --unsquash --compat --cleanenv --pid --no-mount tmp,home \\"
	ApptainerWithCommand    = "apptainer exec --unsquash --compat --cleanenv --pid --no-mount tmp,home \\"
)

var ApptainerTemplate = `{{.Apptainer}}
{{- if .Env}}
--env {{ range $index, $variable := .Env}}{{if $index}},{{end}}{{$variable}}{{end}} \
{{- end}}
{{- if .Bind}}
--bind {{ range $index, $path := .Bind}}{{if $index}},{{end}}{{$path}}{{end}} \
{{- end}}
{{.Image}} 
{{- if .Command}}{{range $index, $cmd := .Command}} "{{$cmd}}"{{end}}{{end -}}
{{- if .Args}}{{range $index, $arg := .Args}} "{{$arg}}"{{end}}{{end -}}
`

// ApptainerTemplateFields container the supported fields for the Apptainer template.
type ApptainerTemplateFields struct {
	/*--
		Mandatory Fields
	--*/
	Apptainer string // Instruction on how to run apptainer

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

const (
	SbatchPreamble = `#!/bin/bash
#SBATCH --job-name={{.Pod.Name}}
#SBATCH --output={{.ScriptsDirectory}}/stdout
#SBATCH --error={{.ScriptsDirectory}}/stderr
{{- if .Options.NTasksPerNode}}
#SBATCH --ntasks-per-node={{.Options.NTasksPerNode}}
{{end}}
{{- if .Options.CPUPerTask}}
#SBATCH --cpus-per-task={{.Options.CPUPerTask}}  # usually, obviously, in the range[1-10]
{{end}}
{{- if .Options.Nodes}}
#SBATCH --nodes={{.Options.Nodes}}
{{end}}
{{- if .Options.MemoryPerNode}}
#SBATCH --mem={{.Options.MemoryPerNode}} # e.g 400GB
{{end}}
{{- if .Options.CustomFlags}}
{{.Options.CustomFlags}}
{{end}}
`

	ResetFlags = `
#### BEGIN SECTION: ResetFlags ####
# Description
# 	If not removed, Flags will be consumed by the nested singularity and overwrite paths.

unset SINGULARITY_BIND
unset SINGULARITY_CONTAINER
unset SINGULARITY_ENVIRONMENT
unset SINGULARITY_NAME
unset APPTAINER_APPNAME
unset APPTAINER_BIND
unset APPTAINER_CONTAINER
unset APPTAINER_ENVIRONMENT
unset APPTAINER_NAME

#### END SECTION: ResetFlags ####`

	FixDNS = `
#### BEGIN SECTION: DNS ####
# Description
# 	Rewire /etc/resolv.conf to point to KubeDNSService

cat > /etc/resolv.conf << DNS_EOF
nameserver {{.KubeDNSService}}
search {{.Pod.Namespace}}.svc.cluster.local svc.cluster.local cluster.local
options ndots:5
DNS_EOF

#### END SECTION: DNS ####`

	DebugInfo = `
#### BEGIN SECTION: DebugInfo ####
# Description
# 	Prints some debugging info

echo "Path:" $(pwd)
echo "Hostname:" $(hostname)
echo "HostIPs:" $(hostname -I)
echo "Date:" $(date)

#### END SECTION: DebugInfo ####`

	RunAndWaitInitContainers = `
#### BEGIN SECTION: InitContainers ####
# Description
# 	Execute the Init containers and wait for completion

{{- if .InitContainers}}
{{ range $index, $container := .InitContainers}}
$({{$container.Command}} 2>> {{$container.StderrPath}} 1>> {{$container.StdoutPath}}; echo $? > {{$container.ExitCodePath}}) &
echo $! > {{$container.JobIDPath}}
{{end}}

wait
{{- end}}
#### END SECTION: InitContainers ####`

	RunAndWaitContainers = `
#### BEGIN SECTION: Containers ####
# Description
# 	Execute the containers

{{- if .Containers}}
{{ range $index, $container := .Containers}}
$({{$container.Command}} 2>> {{$container.StderrPath}} 1>> {{$container.StdoutPath}}; echo $? > {{$container.ExitCodePath}}) &
echo $! > {{$container.JobIDPath}}
{{end}}

wait
{{- end}}
#### END SECTION: Containers ####`
)

// SBatchTemplate provides the context for going from Apptainer jobs to slurm jobs
// Single # are directives to SBATCH
// Double ## are comments.
var SBatchTemplate = SbatchPreamble + `

#### BEGIN SECTION: NestedEnvironment ####
# Description
# 	... Explain what I do ...

cat > {{.ScriptsDirectory}}/virtual-env.sh << "PAUSE_EOF"
echo "Starting Pod $(hostname -I)"
echo $(hostname -I) > {{.PodIPPath}}
` + ResetFlags + FixDNS + DebugInfo + RunAndWaitInitContainers + RunAndWaitContainers + `
PAUSE_EOF

chmod +x  {{.ScriptsDirectory}}/virtual-env.sh 
#### END SECTION: NestedEnvironment ####

#### BEGIN SECTION: Host ####
# Description:
# 	... Explain what I do ...
set -eum pipeline

trap cleanup EXIT
cleanup() {
   exit_code=$?
   echo $exit_code > {{.ScriptsDirectory}}/exitCode
}

echo "Initialize the Pause Environment"
apptainer exec --net --fakeroot --bind /bin,/etc/apptainer,/var/lib/apptainer,/lib,/lib64,/usr \
docker://alpine	{{.ScriptsDirectory}}/virtual-env.sh

#### END SECTION: Host ####`

// SBatchTemplateFields container the supported fields for the submission template.
type SBatchTemplateFields struct {
	/*--
		Mandatory Fields
	--*/
	KubeDNSService string

	Pod types.NamespacedName

	// PodIPPath is where we store the internal Pod's ip.
	PodIPPath string

	// ScriptsDirectory is where we maintain internal scripts
	ScriptsDirectory string

	Options SbatchOptions

	// InitContainers is a list of init container requests to be executed.
	InitContainers []Container

	// Containers is a list of container requests to be executed.
	Containers []Container
}

type Container struct {
	// Command is the evaluated Apptainer command to be executed within sbatch.
	Command string

	// JobIDPath points to the file where the process id of the container is stored.
	// This is used to know when the container has started
	JobIDPath string

	// StdoutPath instruct Slurm to write stdout into the specified path.
	StdoutPath string

	// StdoutPath instruct Slurm to write stderr into the specified path.
	StderrPath string

	// ExitCodePath is the path where the embedded Apptainer command will write its exit code
	ExitCodePath string
}

// SbatchOptions are optional directives to sbatch.
// Optional Fields (marked by a pointer)
type SbatchOptions struct {
	// Nodes request that a minimum of number nodes are allocated to this job.
	Nodes *int

	// NTasksPerNode request that ntasks be invoked on each node
	NTasksPerNode *int

	// CPUPerTask advise the Slurm controller that ensuing job steps will require ncpus number
	// of processors per task.
	CPUPerTask *int

	// MemoryPerNode Specify the real memory required per node.
	MemoryPerNode *string

	// CustomFlags are sbatch that are directly given by the end-user.
	CustomFlags []string
}
