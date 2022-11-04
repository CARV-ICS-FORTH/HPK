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
	v1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/types"
)

/************************************************************

		Apptainer Execution Templates

************************************************************/

const (
	ApptainerPreamble = "apptainer exec  --unsquash --compat --cleanenv --pid --no-mount tmp,home \\"
)

var ApptainerTemplate = ApptainerPreamble + ` 
{{- if .Env}}
--env {{ range $index, $variable := .Env}}{{if $index}},{{end}}{{$variable}}{{end}} \
{{- end}}
{{- if .Bind}}
--bind {{ range $index, $path := .Bind}}{{if $index}},{{end}}{{$path}}{{end}} \
{{- end}}
{{.Image}} 
{{- if .Command}}{{range $index, $cmd := .Command}} "{{$cmd}}"{{end}}{{end}}
{{- if .Args}}{{range $index, $arg := .Args}} "{{$arg}}"{{end}}{{end}}
`

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
#SBATCH --job-name={{.Pod.Name}}
#SBATCH --output={{.StdoutPath}}
#SBATCH --error={{.StderrPath}}
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

#
# Pod (Nested Apptainer) Level
#
cat > {{.ScriptsDirectory}}/pause.sh << "PAUSE_EOF"
echo "Starting Pod $(hostname -I)"

#
# Reset Singularity/Apptainer flags.
# If not removed, they will be consumed by the nested singularity and overwrite paths.
#
echo "Reset Singularity/Apptainer Flags"

unset SINGULARITY_BIND
unset SINGULARITY_CONTAINER
unset SINGULARITY_ENVIRONMENT
unset SINGULARITY_NAME
unset APPTAINER_APPNAME
unset APPTAINER_BIND
unset APPTAINER_CONTAINER
unset APPTAINER_ENVIRONMENT
unset APPTAINER_NAME

#
# Rewire /etc/resolv.conf to point to KubeDNSService
#
echo "Rewrite /etc/resolv.conf"
cat > /etc/resolv.conf << DNS_EOF
nameserver {{.KubeDNSService}}
search {{.Pod.Namespace}}.svc.cluster.local svc.cluster.local cluster.local
options ndots:5
DNS_EOF

#
# Add some tracing info
#
echo $(hostname -I) > {{.PodIPPath}}

echo "Path:" $(pwd)
echo "Hostname:" $(hostname)
echo "HostIPs:" $(hostname -I)
echo "Date:" $(date)

#
# Start the containers (as processes) within the Pause Context
#
{{.ApptainerCommand}}
PAUSE_EOF

chmod +x  {{.ScriptsDirectory}}/pause.sh 

#
# Host Level
#
set -eum pipeline

trap cleanup EXIT
cleanup() {
   exit_code=$?
   echo $exit_code > {{.ExitCodePath}}
}

echo "Initialize the Pause Environment"
apptainer exec --net --fakeroot --bind /bin,/etc/apptainer,/lib,/lib64,/usr,/var/lib/apptainer \
docker://alpine	{{.ScriptsDirectory}}/pause.sh
`

// SBatchTemplateFields container the supported fields for the submission template.
type SBatchTemplateFields struct {
	/*--
		Mandatory Fields
	--*/
	KubeDNSService string

	Pod types.NamespacedName

	// PodIPPath is where we store the internal Pod's ip.
	PodIPPath string

	// ScriptsDirectory is where we maintain intenral scripts
	ScriptsDirectory string

	// ApptainerCommand is the evaluated Apptainer command to be executed within sbatch.
	ApptainerCommand string

	// StdoutPath instruct Slurm to write stdout into the specified path.
	StdoutPath string

	// StdoutPath instruct Slurm to write stderr into the specified path.
	StderrPath string

	// ExitCodePath is the path where the embedded Apptainer command will write its exit code
	ExitCodePath string

	/*--
		Optional Fields (marked by a pointer)
	--*/

	// Nodes request that a minimum of number nodes are allocated to this job.
	Nodes *int

	// NTasksPerNode request that ntasks be invoked on each node
	NTasksPerNode *int

	// CPUPerTask advise the Slurm controller that ensuing job steps will require ncpus number
	// of processors per task.
	CPUPerTask *int

	// MemoryPerNode Specify the real memory required per node.
	MemoryPerNode *string

	// DependencyList defer the start of this job until the specified dependencies have been
	// satisfied completed.
	DependencyList []string

	// CustomFlags are sbatch that are directly given by the end-user.
	CustomFlags []string

	v1.Ingress
}
