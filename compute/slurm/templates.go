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

const SbatchScriptTemplate = `#!/bin/bash
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

set -eum pipeline

#### BEGIN SECTION: VirtualEnvironment Builder ####
# Description
# 	Builds a script for running a Virtual Environment
# 	that resembles the semantics of a Pause Environment.

cat > {{.VirtualEnv.ConstructorPath}} << "PAUSE_EOF"
#!/bin/sh

# Store IP
echo $(hostname -I) > {{.VirtualEnv.IPAddressPath}}

echo "== Virtual Environment Info =="
echo "* User:" $(id)
echo "* Hostname:" $(hostname)
echo "* HostIPs:" $(hostname -I)
echo "* DNS: {{.ComputeEnv.KubeDNS}}"
echo "=============================="

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
#### END SECTION: SetEnv ####

#### BEGIN SECTION: DNS ####
# Description
# 	Rewire /etc/resolv.conf to point to KubeDNS

cat > /etc/resolv.conf << DNS_EOF
search {{.Pod.Namespace}}.svc.cluster.local svc.cluster.local cluster.local
nameserver {{.ComputeEnv.KubeDNS}}
options ndots:5
DNS_EOF
#### END SECTION: DNS ####

#### BEGIN SECTION: InitContainers ####
# Description
# 	Execute the Init containers and wait for completion

{{- if .InitContainers}}
{{ range $index, $container := .InitContainers}}
echo "* Running init container {{$index}}"

$(apptainer {{ $container.ApptainerMode }} --compat --cleanenv --pid --no-mount tmp,home  \ 
{{- if $container.EnvFilePath}}
--env-file {{$container.EnvFilePath}} \
{{- end}}
{{- if $container.Binds}}
--bind {{join "," $container.Binds}} \
{{- end}}
{{$container.Image}}
{{- if $container.Command}}{{range $index, $cmd := $container.Command}} '{{$cmd}}'{{end}}{{end -}}
{{- if $container.Args}}{{range $index, $arg := $container.Args}} '{{$arg}}'{{end}}{{end -}} \
&>> {{$container.LogsPath}}; echo $? > {{$container.ExitCodePath}}) &
echo $! > {{$container.JobIDPath}}

{{end}}
# waiting for init container sto complete
wait
{{- end}}
#### END SECTION: InitContainers ####

#### BEGIN SECTION: Spinup Container Instances ####
# Description
# 	Launch an instance with the name you provide it and lets it run in the background.
#   example: apptainer instance start first.sif instance_name [startscript args...]

{{- if .Containers}}
 {{ range $index, $container := .Containers}}
 echo "* Preparing instance://{{$container.InstanceName}}"


 apptainer instance start --no-init --no-umask --no-eval --no-mount tmp,home --unsquash --writable-tmpfs \
  {{- if $container.Binds}}
  --bind {{join "," $container.Binds}} \
  {{- end}}
 {{$container.Image}} {{$container.InstanceName}}
 {{- end}}

# Do not return as it would release the namespace
sleep infinity
{{- end}}

#### END SECTION: Spinup Container Instances ####
PAUSE_EOF

chmod +x  {{.VirtualEnv.ConstructorPath}}
#### END SECTION: VirtualEnvironment Builder ####

#### BEGIN SECTION: Host Environment ####
# Description
# 	Stuff to run outside the virtual environment


echo "* Initializing the Virtual Environment ..."
apptainer exec --net --network=flannel --fakeroot \
--bind /bin,/etc/apptainer,/var/lib/apptainer,/lib,/lib64,/usr,/etc/passwd,$HOME \
docker://alpine {{.VirtualEnv.ConstructorPath}} &  

echo "* Waiting 5 seconds for the virtual environment to become ready ..."
sleep 5

echo "* Setting up Environment cleaner ..."
VPID=$!

trap teardown EXIT
teardown() {
 echo "* Tearing download the Virtual Environment ..."

 {{- if .Containers}}
 echo "** Stopping apptainer instances. ..."
 {{- range $index, $container := .Containers}} 
 apptainer instance stop {{$container.InstanceName}}
 {{- end}}
 {{- end}}

 echo "** Exiting the Virtual Environment. ..."
 kill ${VPID}
 wait ${VPID}
}

{{- if .Containers}}
{{ range $index, $container := .Containers}}
echo "* Running instance://{{$container.InstanceName}}"

echo instance://{{$container.InstanceName}} > {{$container.JobIDPath}}

$(apptainer {{$container.ApptainerMode}} --compat --cleanenv \ 
{{- if $container.EnvFilePath}}
--env-file {{$container.EnvFilePath}} \
{{- end}}
instance://{{$container.InstanceName}}
{{- if $container.Command}}{{range $index, $cmd := $container.Command}} '{{$cmd}}'{{end}}{{end -}}
{{- if $container.Args}}{{range $index, $arg := $container.Args}} '{{$arg}}'{{end}}{{end -}}
 &>> {{$container.LogsPath}}; echo $? > {{$container.ExitCodePath}}) &
{{- end}}

echo "Waiting for Containers to Complete..."
wait
{{- end}}
#### END SECTION: Host Environment ####
`

// SbatchScriptFields container the supported fields for the submission template.
type SbatchScriptFields struct {
	/*--
		Mandatory Fields
	--*/
	Pod types.NamespacedName

	// VirtualEnv is the equivalent of a Pod.
	VirtualEnv VirtualEnvironmentPaths

	ComputeEnv compute.HPCEnvironment

	// InitContainers is a list of init container requests to be executed.
	InitContainers []Container

	// Containers is a list of container requests to be executed.
	Containers []Container

	Options RequestOptions
}

// The VirtualEnvironmentPaths create lightweight "virtual environments" that resemble "Pods" semantics.
type VirtualEnvironmentPaths struct {
	// ConstructorPath points to the script for creating the virtual environment for Pod.
	ConstructorPath string

	// IPAddressPath is where we store the internal Pod's ip.
	IPAddressPath string

	// JobIDPath points to the file where Slurm Job ID is written.
	// This is used to know when the job has been started.
	JobIDPath string

	// StdoutPath instruct Slurm to write stdout into the specified path.
	StdoutPath string

	// StdoutPath instruct Slurm to write stderr into the specified path.
	StderrPath string

	// ExitCodePath points to the file where Slurm Job Exit Codeis written.
	// This is used to know when the job has been completed.
	ExitCodePath string
}

// The Container creates new within the Pod and resemble the "Container" semantics.
type Container struct {
	// needed for apptainer start.
	InstanceName string // instance://podName_containerName

	Image string // format: REGISTRY://image:tag

	EnvFilePath string

	Binds []string

	Command []string
	Args    []string // space separated args

	ApptainerMode string // exec or run

	// LogsPath instructs process to write stdout and stderr into the specified path.
	LogsPath string

	// JobIDPath points to the file where the process id of the container is stored.
	// This is used to know when the container has started.
	JobIDPath string

	// ExitCodePath is the path where the embedded Container command will write its exit code
	ExitCodePath string
}

// RequestOptions are optional directives to sbatch.
// Optional Fields (marked by a pointer)
type RequestOptions struct {
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

/************************************************************

		Container Execution Templates

************************************************************

const (
	ApptainerBin = "apptainer"

	// ApptainerExec launch the container, run the command that you passed in, and then back out of the container.
	// example: apptainer exec first.sif /bin/bash
	ApptainerExec = ApptainerBin + " exec --compat --cleanenv --pid --no-mount tmp,home \\"

	// ApptainerRun launch the container, perform the runscript within the container, and then back out of the container.
	// example: apptainer run first.sif
	ApptainerRun = ApptainerBin + " run --compat --cleanenv --pid --no-mount tmp,home \\"


	ApptainerStart = ApptainerBin + " start --compat --cleanenv --no-mount tmp,home \\"
)

var ApptainerTemplate = `{{.Apptainer}}
{{- if .EnvironmentFilePath}}
--env-file {{.EnvironmentFilePath}} \
{{- end}}
{{- if .Bind}}
--bind {{ range $index, $path := .Bind}}{{if $index}},{{end}}{{$path}}{{end}} \
{{- end}}
{{.Image}}
{{- if .Command}}{{range $index, $cmd := .Command}} '{{$cmd}}'{{end}}{{end -}}
{{- if .Args}}{{range $index, $arg := .Args}} '{{$arg}}'{{end}}{{end -}}
`

// ApptainerTemplateFields container the supported fields for the Container template.
type ApptainerTemplateFields struct {
	/*--
		Mandatory Fields
	--* /
	Apptainer string // Instruction on how to run apptainer

	/*--
		Optional Fields (marked by a pointer)
	--* /
	InstanceName *string // Used for Instance

	EnvironmentFilePath string   // format: VAR=VALUE
	Bind                []string // format: /hostpath:/containerpath or /hostpath
}
*/

/************************************************************

			Sbatch Templates

************************************************************/

/*






	apptainer exec instance://skata_main 'sh' '-c' 'iperf3 -s'




	echo "Initializing the Virtual Environment ..."


	apptainer exec --net --network=flannel --fakeroot									\
	--bind /bin,/etc/apptainer,/var/lib/apptainer,/lib,/lib64,/usr,/etc/passwd,$HOME	\
	docker://alpine	{{.VirtualEnv.ConstructorPath}}`


	`






	$({{$container.Command}} &>> {{$container.LogsPath}}; echo $? > {{$container.ExitCodePath}}) &
	echo $! > {{$container.JobIDPath}}










	`
		Initialize = `

	`
		SetEnv = `

		FixDNS = `

		RunAndWaitInitContainers = `

		RunAndWaitContainers = `
	#### BEGIN SECTION: Containers ####
	# Description
	# 	Execute the containers

	{{- if .Containers}}
	{{ range $index, $container := .Containers}}
	$({{$container.Command}} &>> {{$container.LogsPath}}; echo $? > {{$container.ExitCodePath}}) &
	echo $! > {{$container.JobIDPath}}
	{{end}}

	echo "Waiting for containers "
	wait
	{{- end}}
	#### END SECTION: Containers ####`
	)

	// SBatchTemplate provides the context for going from Container jobs to slurm jobs
	// Single # are directives to SBATCH
	// Double ## are comments.
	var SBatchTemplate = SbatchScriptTemplate + `

*/
