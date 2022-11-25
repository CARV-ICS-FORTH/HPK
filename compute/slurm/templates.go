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


apptainer={{.ComputeEnv.ApptainerBin}}
echo "Using ApptainerBin: ${apptainer}"


#### BEGIN SECTION: VirtualEnvironment Builder ####
# Description
# 	Builds a script for running a Virtual Environment
# 	that resembles the semantics of a Pause Environment.
cat > {{.VirtualEnv.ConstructorPath}} << "PAUSE_EOF"
#!/bin/bash

# exit when any command fails
set -eum pipeline

# echo an error message before exiting
trap 'echo "\"${BASH_COMMAND}\" command filed with exit code $?.";' EXIT


debug_info() {
	echo "== Virtual Environment Info =="
	echo "* User:" $(id)
	echo "* Hostname:" $(hostname)
	echo "* HostIPs:" $(hostname -I)
	echo "* DNS: {{.ComputeEnv.KubeDNS}}"
	echo "=============================="
}

# Rewire /etc/resolv.conf to point to KubeDNS
handle_dns() {
cat > /etc/resolv.conf << DNS_EOF
search {{.Pod.Namespace}}.svc.cluster.local svc.cluster.local cluster.local
nameserver {{.ComputeEnv.KubeDNS}}
options ndots:5
DNS_EOF
}

# If not removed, Flags will be consumed by the nested Apptainer and overwrite paths.
# https://apptainer.org/user-docs/master/environment_and_metadata.html#environment-from-the-singularity-runtime
unset_env() {
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
}

handle_init_containers() {
{{ range $index, $container := .InitContainers}}
	echo "* Running init container {{$index}}"
	
	$(${apptainer} {{ $container.ApptainerMode }} --compat --cleanenv --pid --no-mount tmp,home  \ 
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

return
}

# Launch an instance with the name you provide it and lets it run in the background.
spinup_instances() {
{{ range $index, $container := .Containers}}
	echo "* Preparing instance://{{$container.InstanceName}}"

	${apptainer} instance start --no-init --no-umask --no-eval --no-mount tmp,home --unsquash --writable-tmpfs \
	{{- if $container.Binds}}
	--bind {{join "," $container.Binds}} \
	{{- end}}
	{{$container.Image}} {{$container.InstanceName}}

{{- end}}

return
}

	# Store IP
	echo $(hostname -I) > {{.VirtualEnv.IPAddressPath}}
	
	debug_info
	
	unset_env
	
	handle_dns
	
	{{- if .InitContainers}}
	handle_init_containers

    echo "waiting for init containers to complete"
	wait
	{{- end}}

	{{- if .Containers}}
	spinup_instances
	{{- end}}

	# Do not return as it would release the namespace
	sleep infinity
PAUSE_EOF
#### END SECTION: VirtualEnvironment Builder ####



#### BEGIN SECTION: Host Environment ####
# Description
# 	Stuff to run outside the virtual environment

cleanup() {
		# continue despite errors
        echo "* Clean up the Virtual Environment ..."
        echo "** Stopping Apptainer instances ..."

		{{ range $index, $container := .Containers}}
		echo "* Stopping instance://{{$container.InstanceName}}"
		${apptainer} instance stop {{$container.InstanceName}} &

		{{- end}}

		wait
}

aborted_shutdown() {
        echo -e "\n ** The Virtual Environment has been aborted ** \n"
        cleanup

		echo "Create a virtual environment dump file"

		echo "Explain the Situations " >> {{.VirtualEnv.SysErrorPath}} 

		exit -1
}

graceful_shutdown() {
        echo -e "\n ** Graceful shutdown the Virtual Environment \n"
        cleanup

        echo "** Exiting the Virtual Environment. ..."
        kill ${VPID}
        wait ${VPID}
}

run_virtual_environment() {
	chmod +x  {{.VirtualEnv.ConstructorPath}}

	VirtualEnvironmentReady=false

	${apptainer} exec --net --network=flannel --fakeroot \
	--bind /bin,/etc/apptainer,/var/lib/apptainer,/lib,/lib64,/usr,/etc/passwd,$HOME \
	docker://alpine {{.VirtualEnv.ConstructorPath}} &

	# return the PID of Apptainer running the virtual environment
	VPID=$!

	echo "* Waiting for the virtual environment to become ready ..."
	while [[ "${VirtualEnvironmentReady}" == false ]]; do 
		echo "* Waiting for virtual-environment to become ready ..."
		sleep 3 
	done
} 

wait_instance_ready() {
{{ range $index, $container := .Containers}}
	echo "* Verify instance://{{$container.InstanceName}}"
	while !  ${apptainer} instance list | grep "{{$container.InstanceName}}"; do 
		echo "retry for {{$container.InstanceName}}"
		${apptainer} instance list
		sleep 3
	done
{{- end}}
}

exec_containers() {
{{ range $index, $container := .Containers}}
	echo "* Running instance://{{$container.InstanceName}}"
	
	echo instance://{{$container.InstanceName}} > {{$container.JobIDPath}}
	
	$(${apptainer} {{$container.ApptainerMode}} --compat --cleanenv \ 
	{{- if $container.EnvFilePath}}
	--env-file {{$container.EnvFilePath}} \
	{{- end}}
	instance://{{$container.InstanceName}}
	{{- if $container.Command}}{{range $index, $cmd := $container.Command}} '{{$cmd}}'{{end}}{{end -}}
	{{- if $container.Args}}{{range $index, $arg := $container.Args}} '{{$arg}}'{{end}}{{end -}}
	 &>> {{$container.LogsPath}}; echo $? > {{$container.ExitCodePath}}) &

{{- end}}
}


# enable job control and notification
# When a background process terminates, the parent receives a SIGCHLD signal; that’s the notification.a
# Any trap on SIGCHLD is executed for each child that exits.
set -emb 

echo "* Setting up Virtual Environment Traps ..."
trap aborted_shutdown SIGCHLD
trap graceful_shutdown EXIT


echo "* Initializing the Virtual Environment ..."
run_virtual_environment

echo "* Waiting until container instances are ready ..."
wait_instance_ready

echo "* == Execute Commands on Containers ==="
exec_containers

echo "* Waiting for Containers to Complete..."
wait
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

	// SysErrorPath indicate a system failure that cause the Pod to fail Immediately, bypassing any other checks.
	SysErrorPath string
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
