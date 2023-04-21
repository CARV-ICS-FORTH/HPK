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
	"github.com/carv-ics-forth/hpk/pkg/resources"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
)

const SlurmScriptTemplate = `#!/bin/bash
#SBATCH --job-name={{.Pod.Name}}
#SBATCH --output={{.VirtualEnv.StdoutPath}}
#SBATCH --error={{.VirtualEnv.StderrPath}}
{{- range $index, $flag := .CustomFlags}}
#SBATCH {{$flag}}
{{end}}

{{- if .ResourceRequest.CPU}}
#SBATCH --ntasks-per-node={{.ResourceRequest.CPU}}
{{end}}

{{- if .ResourceRequest.Memory}}
#SBATCH --mem={{.ResourceRequest.Memory}} 
{{end}} 

# exit when any command fails
set -eum pipeline

export apptainer={{.ComputeEnv.ApptainerBin}}
echo "Using ApptainerBin: ${apptainer}"

#### BEGIN SECTION: VirtualEnvironment Builder ####
# Description
# 	Builds a script for running a Virtual Environment
# 	that resembles the semantics of a Pause Environment.
cat > {{.VirtualEnv.ConstructorFilePath}} << "PAUSE_EOF"
#!/bin/bash

############################
# Auto-Generated Script    #
# Please do not it. 	   #
############################

# exit when any command fails
set -eum pipeline

debug_info() {
	echo "== Virtual Environment Info =="
	echo "* User:" $(id)
	echo "* Hostname:" $(hostname)
	echo "* HostIPs:" $(hostname -I)
	echo "* DNS: {{.ComputeEnv.KubeDNS}}"
	echo "=============================="
}


handle_dns() {
mkdir -p /scratch/etc

# Rewire /scratch/etc/resolv.conf to point to KubeDNS
cat > /scratch/etc/resolv.conf << DNS_EOF
search {{.Pod.Namespace}}.svc.cluster.local svc.cluster.local cluster.local
nameserver {{.ComputeEnv.KubeDNS}}
options ndots:5
DNS_EOF

# Add hostname to known hosts. Required for loopbacks
echo -e "127.0.0.1 localhost" >> /scratch/etc/hosts
echo -e "$(hostname -I) $(hostname)" >> /scratch/etc/hosts
}

# If not removed, Flags will be consumed by the nested Apptainer and overwrite paths.
# https://apptainer.org/user-docs/master/environment_and_metadata.html#environment-from-the-singularity-runtime
reset_env() {
    unset LD_LIBRARY_PATH

	unset SINGULARITY_COMMAND
	unset SINGULARITY_CONTAINER
	unset SINGULARITY_ENVIRONMENT
	unset SINGULARITY_NAME

	unset APPTAINER_APPNAME
	unset APPTAINER_COMMAND
	unset APPTAINER_CONTAINER
	unset APPTAINER_ENVIRONMENT
	unset APPTAINER_NAME

	unset APPTAINER_BIND
	unset SINGULARITY_BIND


	export POD_NAMESPACE={{.Pod.Namespace}}
}

handle_init_containers() {
{{- range $index, $container := .InitContainers}}
	echo "[Virtual] Scheduling Init Container: {{$index}}"
	
	{{- if $container.EnvFilePath}}
	sh -c {{$container.EnvFilePath}} > /scratch/{{$container.InstanceName}}.env
	{{- end}}

	# Mark the beginning of an init job (all get the shell's pid).  
	echo pid://$$ > {{$container.JobIDPath}}

	${apptainer} {{ $container.ApptainerMode }} --cleanenv --pid --compat --no-mount tmp,home --unsquash \
	--bind /scratch/etc/resolv.conf:/etc/resolv.conf,/scratch/etc/hosts:/etc/hosts,{{join "," $container.Binds}} \
	{{- if $container.EnvFilePath}}
	--env-file /scratch/{{$container.InstanceName}}.env \
	{{- end}}
	{{$container.ImageFilePath}}
	{{- if $container.Command}}
		{{- range $index, $cmd := $container.Command}} '{{$cmd}}' {{- end}}
	{{- end -}} 
	{{- if $container.Args}}
		{{range $index, $arg := $container.Args}} '{{$arg}}' {{- end}}
	{{- end }} \
	&>> {{$container.LogsPath}}

	# Mark the ending of an init job.
	echo $? > {{$container.ExitCodePath}}

{{end}}

return 
}

handle_containers() {
{{- range $index, $container := .Containers}}
	echo "[Virtual] Scheduling Container: {{$container.InstanceName}}"

	{{- if $container.EnvFilePath}}
	sh -c {{$container.EnvFilePath}} > /scratch/{{$container.InstanceName}}.env
	{{- end}}

	# Internal fakeroot is needed for appropriate permissions within the container
	$(${apptainer} {{ $container.ApptainerMode }} --cleanenv --pid --compat --no-mount tmp,home --unsquash \
	--bind /scratch/etc/resolv.conf:/etc/resolv.conf,/scratch/etc/hosts:/etc/hosts,{{join "," $container.Binds}} \
	{{- if $container.EnvFilePath}}
	--env-file /scratch/{{$container.InstanceName}}.env \
	{{- end}}
	{{$container.ImageFilePath}}
	{{- if $container.Command}}
		{{- range $index, $cmd := $container.Command}} '{{$cmd}}' {{- end}}
	{{- end -}} 
	{{- if $container.Args}}
		{{- range $index, $arg := $container.Args}} '{{$arg}}' {{- end}}
	{{- end }} \
	&>> {{$container.LogsPath}}; \
	echo $? > {{$container.ExitCodePath}}) &

	echo pid://$! > {{$container.JobIDPath}}
{{- end}}

	echo "[Virtual] All containers have been scheduled"
}

_teardown() {
	lastCommand=$1
	exitCode=$2

	if [[ $exitCode -eq 0 ]]; then
		echo "[Virtual] Gracefully exit the Virtual Environment. All resources will be released."
	else
		echo "[Virtual] **SYSTEMERROR** ${lastCommand} command filed with exit code ${exitCode}" | tee {{.VirtualEnv.SysErrorFilePath}}
		echo "[Virtual] Send Signal to parent (${PARENT}) to inform about the failure"
		echo "env_failed" > /dev/shm/signal_${PARENT}

		echo "[Virtual] Virtual Environment Terminated due to an error. All resources will be released." 
	fi
}

# echo an error message before exiting
trap '_teardown "${BASH_COMMAND}" "$?"'  EXIT

debug_info

handle_dns

reset_env

# Network is ready
echo $(hostname -I) > {{.VirtualEnv.IPAddressPath}}
sync

{{- if .InitContainers}}
echo "[Virtual] Scheduling Init Containers ..."

handle_init_containers
{{- end}}


{{- if .Containers}}
echo "[Virtual] Scheduling Containers ..."

handle_containers

echo "[Virtual] == Listing Scheduled Jobs =="
jobs
echo "[Virtual] ============================"

echo "[Virtual] Signal parent (${PARENT}) that the Virtual Environment is Running"
echo "env_running" > /dev/shm/signal_${PARENT}

echo "[Virtual] ... Waiting for containers to complete...."
wait
{{- end}}
PAUSE_EOF
#### END SECTION: VirtualEnvironment Builder ####


#### BEGIN SECTION: Host Environment ####
# Description
# 	Stuff to run outside the virtual environment

env_failed() {
	echo -e "\n[Host] Signal received: Virtual Environment Has Failed\n"

	reap

	echo "-- Exit with 255--"
	exit 255
}

env_running() {
	echo -e "\n[Host] Signal received: Virtual Environment is Running\n"

	reap

	echo "-- Exit with 0 --"
	exit 0
}

reap() {
	echo "[HOST] Waiting for the Virtual Environment to Exit"
	wait ${VPID}

	echo "[HOST] Cleanup Signal files"
	rm /dev/shm/signal_${PPID}
}

sigdown() {
	echo -e "\n[Host] Shutting down the Virtual Environment, got signal \n"

	echo -e "\n[Host] Kill Virtual Environment \n"
	kill -TERM ${VPID} 

	# sync filesystems
	sync
}


echo "== Submit Environment =="
chmod +x  {{.VirtualEnv.ConstructorFilePath}}

echo "[Host] Setting Signal Traps for Virtual Environment [Abort (TERM,INT), Failed (USR1), Running (USR2)]..."
trap sigdown SIGTERM SIGINT

echo "[Host] Starting the constructor the Virtual Environment ..."

# External fakeroot is needed for the networking  
${apptainer} exec  --net --fakeroot --scratch /scratch \
--apply-cgroups {{.VirtualEnv.CgroupFilePath}}		\
--env PARENT=${PPID}								 \
--bind $HOME		  								 \
--hostname {{.Pod.Name}}							 \
docker://icsforth/pause {{.VirtualEnv.ConstructorFilePath}} &

# return the PID of Apptainer running the virtual environment
VPID=$!

# Wait on a dummy descriptor. When the signal arrives, the read will get interrupted
echo "[Host] Waiting for virtual environment to become ready ..."
tail -F /dev/shm/signal_${PPID} 2>/dev/null | grep -q "env_"

# dispatch to the appropriate function
$(cat /dev/shm/signal_${PPID})

#### END SECTION: Host Environment ####
`

// JobFields provide the inputs to SlurmScriptTemplate.
type JobFields struct {
	Pod types.NamespacedName

	// VirtualEnv is the equivalent of a Pod.
	VirtualEnv VirtualEnvironmentPaths

	ComputeEnv compute.HPCEnvironment

	// InitContainers is a list of init container requests to be executed.
	InitContainers []Container

	// Containers is a list of container requests to be executed.
	Containers []Container

	// ResourceRequest are reserved resources for the job.
	ResourceRequest resources.ResourceList

	// CustomFlags are flags given by the user via 'slurm.hpk.io/flags' annotations
	CustomFlags []string
}

// The VirtualEnvironmentPaths create lightweight "virtual environments" that resemble "Pods" semantics.
type VirtualEnvironmentPaths struct {
	// CgroupFilePath points to the cgroup configuration for the virtual environment.
	CgroupFilePath string

	// ConstructorFilePath points to the script for creating the virtual environment for Pod.
	ConstructorFilePath string

	// IPAddressPath is where we store the internal Pod's ip.
	IPAddressPath string

	// StdoutPath instruct Slurm to write stdout into the specified path.
	StdoutPath string

	// StdoutPath instruct Slurm to write stderr into the specified path.
	StderrPath string

	// SysErrorFilePath indicate a system failure that cause the Pod to fail Immediately, bypassing any other checks.
	SysErrorFilePath string
}

// The Container creates new within the Pod and resemble the "Container" semantics.
type Container struct {
	// needed for apptainer start.
	InstanceName string // instance://podName_containerName

	ImageFilePath string // format: REGISTRY://image:tag

	EnvFilePath string

	Binds []string

	Command []string

	Args []string // space separated args

	ApptainerMode string // exec or run

	// LogsPath instructs process to write stdout and stderr into the specified path.
	LogsPath string

	// JobIDPath points to the file where the process id of the container is stored.
	// This is used to know when the container has started.
	JobIDPath string

	// ExitCodePath is the path where the embedded Container command will write its exit code
	ExitCodePath string
}

// GenerateEnvTemplate is used to generate environment variables.
// This is needed for variables that consume information from the downward API (like .status.podIP)
const GenerateEnvTemplate = `#!/bin/bash

{{- range $index, $variable := .Variables}}
{{- if eq $variable.Value ".status.podIP"}}
echo {{$variable.Name}}=$(ip route get 1 | sed -n 's/.*src \([0-9.]\+\).*/\1/p')
{{ else }}
echo {{$variable.Name}}=\''{{$variable.Value}}'\'
{{- end}}
{{- end}}
`

// GenerateEnvFields provide the inputs to GenerateEnvTemplate.
type GenerateEnvFields = struct {
	Variables []corev1.EnvVar
}
