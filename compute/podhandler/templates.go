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

package podhandler

import (
	"fmt"
	"strings"
	"text/template"

	"github.com/Masterminds/sprig"
	"github.com/alessio/shellescape"
	"github.com/carv-ics-forth/hpk/compute"
	"github.com/carv-ics-forth/hpk/pkg/process"
	"github.com/carv-ics-forth/hpk/pkg/resources"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
)

var genericMap = map[string]interface{}{
	"param": EscapeSingleQuote,
}

// ParseTemplate returns a custom 'text/template' enhanced with functions for processing HPK templates.
func ParseTemplate(text string) (*template.Template, error) {
	return template.New("").
		Funcs(sprig.TxtFuncMap()).
		Funcs(genericMap).
		Option("missingkey=error").Parse(text)
}

func EscapeSingleQuote(str ...interface{}) string {
	out := make([]string, 0, len(str))
	for _, s := range str {
		if s != nil {
			// wrap fields into single quotes, but escape any single quotes from the payload.
			// escaped := strings.ReplaceAll(strval(s), "'", "\\'")
			escaped := shellescape.Quote(strval(s))
			out = append(out, fmt.Sprintf("%v", escaped))
		}
	}
	return strings.Join(out, " ")
}

func strval(v interface{}) string {
	switch v := v.(type) {
	case string:
		return v
	case []byte:
		return string(v)
	case error:
		return v.Error()
	case fmt.Stringer:
		return v.String()
	default:
		return fmt.Sprintf("%v", v)
	}
}

/*
	PauseScriptTemplate provides the template for building pods.

Remarks:

	--userns is need to maintain the user's permissions.
	--pid is not needed in order for different containers in the same pod to share the same pid space
*/
const PauseScriptTemplate = `#!/bin/bash

############################
# Auto-Generated Script    #
# Please do not edit. 	   #
############################

set -eum pipeline

function debug_info() {
	echo -e "\n"
	echo "=============================="
	echo " Compute Environment Info"
	echo "=============================="
	echo "* DNS: {{.HostEnv.KubeDNS}}"
	echo "* PodDir: {{.VirtualEnv.PodDirectory}}"
	echo "=============================="
	echo -e "\n"
	echo "=============================="
	echo " Virtual Environment Info"
	echo "=============================="
	echo "* Host: $(hostname)"
	echo "* IP: $(hostname -I)"
	echo "* User: $(id)"
	echo "=============================="
	echo -e "\n"
}

handle_dns() {
	mkdir -p /scratch/etc
	
# Rewire /scratch/etc/resolv.conf to point to KubeDNS
cat > /scratch/etc/resolv.conf << DNS_EOF
search {{.Pod.Namespace}}.svc.cluster.local svc.cluster.local cluster.local
nameserver {{.HostEnv.KubeDNS}}
options ndots:5
DNS_EOF
	
	# Add hostname to known hosts. Required for loopback
	echo -e "127.0.0.1 localhost" >> /scratch/etc/hosts
	echo -e "$(hostname -I) $(hostname)" >> /scratch/etc/hosts
}

# If not removed, Flags will be consumed by the nested Singularity and overwrite paths.
# https://docs.sylabs.io/guides/3.11/user-guide/environment_and_metadata.html
function reset_env() {
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
}

function cleanup() {
	lastCommand=$1
	exitCode=$2

	echo "[Virtual] Ensure all background jobs are terminated".
	wait

	if [[ $exitCode -eq 0 ]]; then
		echo "[Virtual] Gracefully exit the Virtual Environment. All resources will be released."
	else
		echo "[Virtual] **SYSTEMERROR** ${lastCommand} command filed with exit code ${exitCode}" | tee {{.VirtualEnv.SysErrorFilePath}}
	fi

	exit ${exitCode}
}

function handle_init_containers() {
{{range $index, $container := .InitContainers}}
	####################
	##  New Container  #
	####################

	echo "[Virtual] Spawning InitContainer: {{$container.InstanceName}}"
	 
	{{- if $container.EnvFilePath}}
	sh -c {{$container.EnvFilePath}} > /scratch/{{$container.InstanceName}}.env
	{{- end}}

	# Mark the beginning of an init job (all get the shell's pid).  
	echo pid://$$ > {{$container.JobIDPath}}


	$(apptainer {{ $container.ExecutionMode }} --cleanenv --writable-tmpfs --no-mount home --unsquash \
	{{- if $container.RunAsUser}}
	--security uid:{{$container.RunAsUser}},gid:{{$container.RunAsUser}} --userns \
	{{- end}}
	{{- if $container.RunAsGroup}}
	--security gid:{{$container.RunAsGroup}} --userns \
	{{- end}}
	--bind /scratch/etc/resolv.conf:/etc/resolv.conf,/scratch/etc/hosts:/etc/hosts,{{join "," $container.Binds}} \
	{{- if $container.EnvFilePath}}
	--env-file /scratch/{{$container.InstanceName}}.env \
	{{- end}}
	{{$container.ImageFilePath}}
	{{- if $container.Command}}
		{{- range $index, $cmd := $container.Command}} {{$cmd | param}} {{- end}}
	{{- end -}} 
	{{- if $container.Args}}
		{{range $index, $arg := $container.Args}} {{$arg | param}} {{- end}}
	{{- end }} \
	&>> {{$container.LogsPath}})

	# Mark the ending of an init job.
	echo $? > {{$container.ExitCodePath}}
{{end}}

	echo "[Virtual] All InitContainers have been completed."
	return 
}

function handle_containers() {
{{range $index, $container := .Containers}}
	####################
	##  New Container  # 
	####################

	{{- if $container.EnvFilePath}}
	sh -c {{$container.EnvFilePath}} > /scratch/{{$container.InstanceName}}.env
	{{- end}}

	$(apptainer {{ $container.ExecutionMode }} \
	--nv --cleanenv --writable-tmpfs --no-mount home --unsquash \
	{{- if $container.RunAsUser}}
	--security uid:{{$container.RunAsUser}},gid:{{$container.RunAsUser}} --userns \
	{{- end}}
	{{- if $container.RunAsGroup}}
	--security gid:{{$container.RunAsGroup}} --userns \
	{{- end}}
	--bind /scratch/etc/resolv.conf:/etc/resolv.conf,/scratch/etc/hosts:/etc/hosts,{{join "," $container.Binds}} \
	{{- if $container.EnvFilePath}}
	--env-file /scratch/{{$container.InstanceName}}.env \
	{{- end}}
	{{$container.ImageFilePath}}
	{{- if $container.Command}}
		{{- range $index, $cmd := $container.Command}} {{$cmd | param}} {{- end}}
	{{- end -}} 
	{{- if $container.Args}}
		{{- range $index, $arg := $container.Args}} {{$arg | param}} {{- end}}
	{{- end }} \
	&>> {{$container.LogsPath}}; \
	echo $? > {{$container.ExitCodePath}}) &

	pid=$!
	echo pid://${pid} > {{$container.JobIDPath}}
	echo "[Virtual] Container started: {{$container.InstanceName}} ${pid}"
{{end}}

	######################
	##  Wait Containers  #
	######################

	echo "[Virtual] ... Waiting for containers to complete ..."
	wait  || echo "[Virtual] ... wait failed with error: $?"
	echo "[Virtual] ... Containers terminated ..."
}



debug_info

echo "[Virtual] Resetting Environment ..."
reset_env

echo "[Virtual] Announcing IP ..."
echo $(hostname -I) > {{.VirtualEnv.IPAddressPath}}

echo "[Virtual] Setting DNS ..."
handle_dns

echo "[Virtual] Setting Cleanup Handler ..."
trap 'cleanup "${BASH_COMMAND}" "$?"'  EXIT

{{if gt (len .InitContainers) 0 }} handle_init_containers {{end}}

{{if gt (len .Containers) 0 }} handle_containers {{end}}
`

const HostScriptTemplate = `#!/bin/bash
#SBATCH --job-name={{.Pod.Name}}
#SBATCH --output={{.VirtualEnv.StdoutPath}}
#SBATCH --error={{.VirtualEnv.StderrPath}}
{{- range $index, $flag := .CustomFlags}}
#SBATCH {{$flag}}
{{end}}
#SBATCH --signal=B:TERM@60 # tells the controller
                            # to send SIGTERM to the job 60 secs
                            # before its time ends to give it a
                            # chance for better cleanup.

{{- if .ResourceRequest.CPU}}
#SBATCH --ntasks-per-node={{.ResourceRequest.CPU}}
{{end}}

{{- if .ResourceRequest.GPU}}
module load cuda
module load nvidia
#SBATCH --gres=gpu:{{.ResourceRequest.GPU}}
{{end}}

{{- if .ResourceRequest.Memory}}
#SBATCH --mem={{.ResourceRequest.Memory}} 
{{end}} 

#### BEGIN SECTION: VirtualEnvironment Builder ####
# Description
# 	Builds a script for running a Virtual Environment
# 	that resembles the semantics of a Pause Environment.
cat > {{.VirtualEnv.ConstructorFilePath}} << 'PAUSE_EOF'
` + PauseScriptTemplate + `
PAUSE_EOF
#### END SECTION: VirtualEnvironment Builder ####


#### BEGIN SECTION: Host Environment ####
# Description
# 	Stuff to run outside the virtual environment

# exit when any command fails
#set -um pipeline
set -u

echo "[Host] Starting the Constructor for the Virtual Environment ..."
chmod +x  {{.VirtualEnv.ConstructorFilePath}}

export workdir=/tmp/{{.Pod.Namespace}}_{{.Pod.Name}}
echo "[Host] Creating workdir: ${workdir} "
mkdir -p ${workdir}

# --network-args "portmap=8080:80/tcp"
# --container is needed to start a separate /dev/sh
#exec {{$.HostEnv.ApptainerBin}} exec --nv --containall --net --fakeroot --scratch /scratch --workdir ${workdir} \
#{{- if .HostEnv.EnableCgroupV2}}
#--apply-cgroups {{.VirtualEnv.CgroupFilePath}} 		\
#{{- end}}
#--env PARENT=${PPID}								\
#--bind $HOME,/tmp										\
#--hostname {{.Pod.Name}}							\
#{{$.PauseImageFilePath}} sh -ci {{.VirtualEnv.ConstructorFilePath}} ||
#echo "[HOST] **SYSTEMERROR** apptainer exited with code $?" | tee {{.VirtualEnv.SysErrorFilePath}}

export APPTAINERENV_KUBEDNS_IP={{.HostEnv.KubeDNS}}

exec {{$.HostEnv.ApptainerBin}} exec --nv --containall --net --fakeroot --scratch /scratch --workdir ${workdir} \
{{- if .HostEnv.EnableCgroupV2}}
--apply-cgroups {{.VirtualEnv.CgroupFilePath}} 		\
{{- end}}
--env PARENT=${PPID}								\
--bind $HOME/.hpk-master/kubernetes:/k8s-data			\
--bind /etc/apptainer/apptainer.conf				\
--bind $HOME,/tmp									\
--hostname {{.Pod.Name}}							\
{{$.PauseImageFilePath}} /usr/local/bin/hpk-pause -namespace {{.Pod.Namespace}} -pod {{.Pod.Name}} ||
echo "[HOST] **SYSTEMERROR** hpk-pause exited with code $?" | tee {{.VirtualEnv.SysErrorFilePath}}

#### END SECTION: Host Environment ####
`

// JobFields provide the inputs to HostScriptTemplate.
type JobFields struct {
	Pod types.NamespacedName

	// PauseImageFilePath contains the name of the image for the pause container.
	PauseImageFilePath string

	// VirtualEnv is the equivalent of a Pod.
	VirtualEnv compute.VirtualEnvironment

	HostEnv compute.HostEnvironment

	// InitContainers is a list of init container requests to be executed.
	InitContainers []Container

	// Containers is a list of container requests to be executed.
	Containers []Container

	// ResourceRequest are reserved resources for the job.
	ResourceRequest resources.ResourceList

	// CustomFlags are flags given by the user via 'slurm.hpk.io/flags' annotations
	CustomFlags []string
}

// The Container creates new within the Pod and resemble the "Container" semantics.
type Container struct {
	// needed for apptainer start.
	InstanceName string // instance://podName_containerName

	// The UID to run the entrypoint of the container process.
	// May also be set in PodSecurityContext.  If set in both SecurityContext and
	// PodSecurityContext, the value specified in SecurityContext takes precedence.
	RunAsUser int64

	// The GID to run the entrypoint of the container process.
	// May also be set in PodSecurityContext.  If set in both SecurityContext and
	// PodSecurityContext, the value specified in SecurityContext takes precedence.
	RunAsGroup int64

	ImageFilePath string // format: REGISTRY://image:tag

	EnvFilePath string

	Binds []string

	Command []string

	Args []string // space separated args

	ExecutionMode string // exec or run

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

// ValidateScript runs the bash -n <filename.sh> to validate the generated script.
func ValidateScript(filepath string) error {
	_, err := process.Execute("bash", "-n", filepath)
	return err
}
