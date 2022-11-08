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
	"github.com/carv-ics-forth/hpk/compute"
	"k8s.io/apimachinery/pkg/types"
)

/************************************************************

		Process Execution Templates

************************************************************/

const (
	ApptainerBin = "apptainer"

	// ApptainerExec launch the container, run the command that you passed in, and then back out of the container.
	// example: apptainer exec first.sif /bin/bash
	ApptainerExec = ApptainerBin + " exec --unsquash --compat --cleanenv --pid --no-mount tmp,home \\"

	// ApptainerRun launch the container, perform the runscript within the container, and then back out of the container.
	// example: apptainer run first.sif
	ApptainerRun = ApptainerBin + " run --unsquash --compat --cleanenv --pid --no-mount tmp,home \\"
)

var ApptainerTemplate = `{{.Apptainer}}
{{- if .Environment}}
--env {{ range $index, $variable := .Environment}}{{if $index}},{{end}}{{$variable}}{{end}} \
{{- end}}
{{- if .Bind}}
--bind {{ range $index, $path := .Bind}}{{if $index}},{{end}}{{$path}}{{end}} \
{{- end}}
{{.Image}} 
{{- if .Command}}{{range $index, $cmd := .Command}} "{{$cmd}}"{{end}}{{end -}}
{{- if .Args}}{{range $index, $arg := .Args}} "{{$arg}}"{{end}}{{end -}}
`

// ApptainerTemplateFields container the supported fields for the Process template.
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

	Environment []string // format: VAR=VALUE
	Bind        []string // format: /hostpath:/containerpath or /hostpath
}

/************************************************************

			Sbatch Templates

************************************************************/

const (
	SbatchPreamble = `#!/bin/bash
#SBATCH --job-name={{.Pod.Name}}
#SBATCH --output={{.VirtualEnv.StdoutPath}}
#SBATCH --error={{.VirtualEnv.StderrPath}}
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

	SetEnv = `
#### BEGIN SECTION: SetEnv ####
# Description
# 	If not removed, Flags will be consumed by the nested singularity and overwrite paths.
# Source
# 	https://apptainer.org/user-docs/master/environment_and_metadata.html#environment-from-the-singularity-runtime

unset SINGULARITY_COMMAND
unset SINGULARITY_CONTAINER
unset SINGULARITY_ENVIRONMENT
unset SINGULARITY_NAME
unset SINGULARITY_BIND

unset APPTAINER_APPNAME
unset APPTAINER_COMMAND
unset APPTAINER_CONTAINER
unset APPTAINER_ENVIRONMENT
unset APPTAINER_NAME
unset APPTAINER_BIND

#### END SECTION: SetEnv ####`

	FixDNS = `
#### BEGIN SECTION: DNS ####
# Description
# 	Rewire /etc/resolv.conf to point to KubeDNS

cat > /etc/resolv.conf << DNS_EOF
nameserver {{.ComputeEnv.KubeDNS}}
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
echo $! > {{$container.IDPath}}
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
echo $! > {{$container.IDPath}}
{{end}}

wait
{{- end}}
#### END SECTION: Containers ####`
)

// SBatchTemplate provides the context for going from Process jobs to slurm jobs
// Single # are directives to SBATCH
// Double ## are comments.
var SBatchTemplate = SbatchPreamble + `

#### BEGIN SECTION: NestedEnvironment ####
# Description
# 	... Explain what I do ...

cat > {{.VirtualEnv.ConstructorPath}} << "PAUSE_EOF"
echo "Starting VirtualEnvironment $(hostname -I)"
echo $(hostname -I) > {{.VirtualEnv.IPAddressPath}}
` + SetEnv + FixDNS + DebugInfo + RunAndWaitInitContainers + RunAndWaitContainers + `
PAUSE_EOF

chmod +x  {{.VirtualEnv.ConstructorPath}} 
#### END SECTION: NestedEnvironment ####

#### BEGIN SECTION: Host ####
# Description:
# 	... Explain what I do ...
set -eum pipeline

trap cleanup EXIT
cleanup() {
   exit_code=$?
   echo $exit_code > {{.VirtualEnv.ExitCodePath}}
}

echo "Initializing the Pause Environment ..."
apptainer exec --net --fakeroot --bind /bin,/etc/apptainer,/var/lib/apptainer,/lib,/lib64,/usr,/etc/passwd,$HOME \
docker://alpine	 {{.VirtualEnv.ConstructorPath}}

#### END SECTION: Host ####`

// SBatchTemplateFields container the supported fields for the submission template.
type SBatchTemplateFields struct {
	/*--
		Mandatory Fields
	--*/
	Pod types.NamespacedName

	ComputeEnv compute.HPCEnvironment

	// VirtualEnv is the equivalent of a Pod.
	VirtualEnv VirtualEnvironment

	// InitContainers is a list of init container requests to be executed.
	InitContainers []Process

	// Containers is a list of container requests to be executed.
	Containers []Process

	Options SbatchOptions
}

// The VirtualEnvironment create lightweight "virtual environments" that resemble "Pods" semantics.
type VirtualEnvironment struct {
	// ConstructorPath points to the script for creating the virtual environment for VirtualEnvironment.
	ConstructorPath string

	// IPAddressPath is where we store the internal VirtualEnvironment's ip.
	IPAddressPath string

	// IDPath points to the file where Slurm Job ID is written.
	// This is used to know when the job has been started.
	IDPath string

	// StdoutPath instruct Slurm to write stdout into the specified path.
	StdoutPath string

	// StdoutPath instruct Slurm to write stderr into the specified path.
	StderrPath string

	// ExitCodePath points to the file where Slurm Job Exit Codeis written.
	// This is used to know when the job has been completed.
	ExitCodePath string
}

// The Process creates new within the VirtualEnvironment and resemble the "Process" semantics.
type Process struct {
	// Command is the evaluated Process command to be executed within sbatch.
	Command string

	// IDPath points to the file where the process id of the container is stored.
	// This is used to know when the container has started.
	IDPath string

	// StdoutPath instruct Slurm to write stdout into the specified path.
	StdoutPath string

	// StdoutPath instruct Slurm to write stderr into the specified path.
	StderrPath string

	// ExitCodePath is the path where the embedded Process command will write its exit code
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
