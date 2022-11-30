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

# exit when any command fails
set -eum pipeline

export apptainer={{.ComputeEnv.ApptainerBin}}
echo "Using ApptainerBin: ${apptainer}"

#### BEGIN SECTION: VirtualEnvironment Builder ####
# Description
# 	Builds a script for running a Virtual Environment
# 	that resembles the semantics of a Pause Environment.
cat > {{.VirtualEnv.ConstructorPath}} << "PAUSE_EOF"
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

# Rewire /etc/resolv.conf to point to KubeDNS
handle_dns() {
cat > /etc/resolv.conf << DNS_EOF
search {{.Pod.Namespace}}.svc.cluster.local svc.cluster.local cluster.local
nameserver {{.ComputeEnv.KubeDNS}}
nameserver 8.8.8.8
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
{{- range $index, $container := .InitContainers}}
	echo "[Virtual] Scheduling Init Container: {{$index}}"
	
	${apptainer} {{ $container.ApptainerMode }} --compat --no-mount tmp,home --userns \ 
	{{- if $container.EnvFilePath}}
	--env-file {{$container.EnvFilePath}} \
	{{- end}}
	{{- if $container.Binds}}
	--bind {{join "," $container.Binds}} \
	{{- end}}
	{{$container.Image}}
	{{- if $container.Command}}{{range $index, $cmd := $container.Command}} '{{$cmd}}'{{end}}{{end -}}
	{{- if $container.Args}}{{range $index, $arg := $container.Args}} '{{$arg}}'{{end}}{{end -}} \
	&>> {{$container.LogsPath}}; echo $? > {{$container.ExitCodePath}} &

	echo pid://$! > {{$container.JobIDPath}}
{{end}}

return 
}

handle_containers() {
{{- range $index, $container := .Containers}}
	echo "[Virtual] Scheduling Container: {{$container.InstanceName}}"

	$(${apptainer} {{$container.ApptainerMode}} --compat --no-mount tmp,home --userns \ 
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
		echo "[Virtual] **SYSTEMERROR** ${lastCommand} command filed with exit code ${exitCode}" | tee {{.VirtualEnv.SysErrorPath}}
		echo "[Virtual] Send Signal to parent (${PARENT}) to inform about the failure"
		echo "env_failed" > /dev/shm/signal_${PARENT}

		echo "[Virtual] Virtual Environment Terminated due to an error. All resources will be released." 
	fi
}

# echo an error message before exiting
trap '_teardown "${BASH_COMMAND}" "$?"'  EXIT

# Store IP
echo $(hostname -I) > {{.VirtualEnv.IPAddressPath}}

debug_info

unset_env

handle_dns

{{- if .InitContainers}}
echo "[Virtual] Scheduling Init Containers ..."

handle_init_containers

echo "[Virtual] Waiting for init containers to complete"
wait
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
env_abort() {
	echo -e "\n[Host] Shutdown the Virtual Environment \n"

	echo -e "\n[Host] Kill Virtual Environment \n"
	kill -TERM ${VPID} 
}

env_failed() {
	echo -e "\n[Host] Signal received: Virtual Environment Has Failed\n"

	echo "[HOST] Waiting for the Virtual Environment to Exit"
	wait ${VPID}

	echo "[HOST] Cleanup Signal files"
	rm /dev/shm/signal_${PPID}

	echo "-- Exit with 1--"
	exit -1
}

env_running() {
	echo -e "\n[Host] ** Environment Is Running.  **\n"
	wait

	echo "[HOST] Cleanup Signal files"
	rm /dev/shm/signal_${PPID}

	echo "-- Exit with 0 --"
	exit 0
}


echo "== Submit Environment =="
chmod +x  {{.VirtualEnv.ConstructorPath}}

echo "[Host] Setting Signal Traps for Virtual Environment [Aborted (TERM,KILL,QUIT,INT), Failed (USR1), Running (USR2)]..."
trap env_abort SIGTERM

echo "[Host] Starting the constructor the Virtual Environment ..."

${apptainer} exec --net --network=flannel --fakeroot \
--env PARENT=${PPID}								 \
--hostname {{.Pod.Name}}							 \
--bind /bin,/etc/apptainer,/var/lib/apptainer,/lib,/lib64,/usr,/etc/passwd,$HOME,/run/shm \
docker://alpine {{.VirtualEnv.ConstructorPath}} &

# return the PID of Apptainer running the virtual environment
VPID=$!

# Wait on a dummy descriptor. When the signal arrives, the read will get interrupted
echo "[Host] Waiting for virtual environment to become ready ..."
tail -F /dev/shm/signal_${PPID} 2>/dev/null | grep -q "env_"

# dispatch to the appropriate function
$(cat /dev/shm/signal_${PPID})

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
