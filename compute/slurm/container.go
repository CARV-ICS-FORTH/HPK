// Copyright © 2023 FORTH-ICS
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
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/Masterminds/sprig"
	"github.com/carv-ics-forth/hpk/compute"
	"github.com/carv-ics-forth/hpk/compute/slurm/image"
	"github.com/carv-ics-forth/hpk/compute/slurm/job"
	kubecontainer "github.com/carv-ics-forth/hpk/pkg/container"
	"github.com/carv-ics-forth/hpk/pkg/hostutil"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	mounter "k8s.io/utils/mount"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// buildContainer replicates the behavior of
// https://github.com/kubernetes/kubernetes/blob/master/pkg/kubelet/kuberuntime/kuberuntime_container.go
func (h *podHandler) buildContainer(container *corev1.Container, containerStatus *corev1.ContainerStatus) (Container, error) {
	/*---------------------------------------------------
	 * Determine the effective security context
	 *---------------------------------------------------*/
	effectiSecurityContext := DetermineEffectiveSecurityContext(h.Pod, container)
	uid, gid := DetermineEffectiveRunAsUser(effectiSecurityContext)

	/*---------------------------------------------------
	 * Generate Environment Variables
	 *---------------------------------------------------*/
	envFileTemplate, err := template.New(h.Name).
		Funcs(sprig.TxtFuncMap()).
		Option("missingkey=error").Parse(GenerateEnvTemplate)
	if err != nil {
		compute.SystemPanic(err, "generate env template error")
	}

	fields := GenerateEnvFields{
		Variables: append(h.podEnvVariables, container.Env...),
	}

	envFileContent := strings.Builder{}

	if err := envFileTemplate.Execute(&envFileContent, fields); err != nil {
		/*-- since both the template and fields are internal to the code, the evaluation should always succeed	--*/
		compute.SystemPanic(err, "failed to evaluate sbatch template")
	}

	envfilePath := h.podDirectory.Container(container.Name).EnvFilePath()

	if err := os.WriteFile(envfilePath, []byte(envFileContent.String()), compute.PodGlobalDirectoryPermissions); err != nil {
		compute.SystemPanic(err, "cannot write env file for container '%s' of pod '%s'", container, h.podKey)
	}

	/*---------------------------------------------------
	 * Prepare Mountpoints
	 *---------------------------------------------------*/
	binds := make([]string, len(container.VolumeMounts))

	// check the code from https://github.com/kubernetes/kubernetes/blob/master/pkg/kubelet/kubelet_pods.go#L196
	for i, mount := range container.VolumeMounts {
		hostPath := filepath.Join(h.podDirectory.VolumeDir(), mount.Name)

		subPath := mount.SubPath
		if mount.SubPathExpr != "" {
			subPath, err = kubecontainer.ExpandContainerVolumeMounts(mount, h.podEnvVariables)
			if err != nil {
				compute.SystemPanic(err, "cannot expand env variables for container '%s' of pod '%s'", container, h.podKey)
			}
		}

		if subPath != "" {
			if filepath.IsAbs(subPath) {
				return Container{}, errors.Errorf("error SubPath '%s' must not be an absolute path", subPath)
			}

			subPathFile := filepath.Join(hostPath, subPath)

			subPathFileExists, err := mounter.PathExists(subPathFile)
			if err != nil {
				compute.SystemPanic(err, "Could not determine if subPath exists. mount:'%v'", mount)
			}

			if !subPathFileExists {
				// Create the sub path now because if it's auto-created later when referenced, it may have an
				// incorrect ownership and mode.
				// The placeholder should normally be of the same type (dir or file) as the bind target.
				// However, at this point we do not have access to the bind.
				// For this reason, we follow the convention that dir should be marked "/path/subpath/" whereas
				// files should be marked as "/path/subpath".
				//
				// For the particular case of Argo, we know that "0" are always dirs.
				if mount.SubPath == "0" {
					if err := hostutil.SafeMakeDir(subPath, hostPath, compute.PodGlobalDirectoryPermissions); err != nil {
						compute.SystemPanic(err, "failed to create dir placeholder. subpath:'%s'", subPathFile)
					}
				} else {
					// A file is enough for all possible targets (symlink, device, pipe,
					// socket, ...), bind-mounting them into a file correctly changes type
					// of the target file.
					if err = os.WriteFile(subPathFile, []byte{}, compute.PodGlobalDirectoryPermissions); err != nil {
						compute.SystemPanic(err, "failed to create placeholder. subpath:'%s'", subPathFile)
					}
				}
			}

			// mount the subpath
			hostPath = subPathFile
		}

		accessMode := "rw"
		if mount.ReadOnly {
			accessMode = "ro"
		}

		binds[i] = hostPath + ":" + mount.MountPath + ":" + accessMode
	}

	/*---------------------------------------------------
	 * Prepare Container Paths
	 *---------------------------------------------------*/
	containerID := fmt.Sprintf("%s_%s_%s", h.Pod.GetNamespace(), h.Pod.GetName(), container.Name)

	imageID, err := image.PullImage(image.Docker, container.Image)
	if err != nil {
		compute.SystemPanic(err, "ImagePull error. Image: %s", container.Image)
	}

	executionMode := func() string {
		if container.Command == nil {
			return "run"
		} else {
			return "exec"
		}
	}()

	/*---------------------------------------------------
	 * Prepare fields for Container Template
	 *---------------------------------------------------*/
	containerPath := h.podDirectory.Container(container.Name)

	c := Container{
		InstanceName:  containerID,
		RunAsUser:     uid,
		RunAsGroup:    gid,
		ImageFilePath: imageID,
		EnvFilePath:   containerPath.EnvFilePath(),
		Binds:         binds,
		Command:       kubecontainer.ExpandContainerCommandOnlyStatic(container.Command, container.Env),
		Args:          kubecontainer.ExpandContainerCommandOnlyStatic(container.Args, container.Env),
		ExecutionMode: executionMode,
		LogsPath:      containerPath.LogsPath(),
		JobIDPath:     containerPath.IDPath(),
		ExitCodePath:  containerPath.ExitCodePath(),
	}

	/*---------------------------------------------------
	 * Update Container Status Fields
	 *---------------------------------------------------*/
	containerStatus.Name = container.Name
	containerStatus.ContainerID = containerID

	containerStatus.Image = container.Image
	containerStatus.ImageID = imageID

	return c, nil
}

/*************************************************************

		Load Container status from the FS

*************************************************************/

func SyncContainerStatuses(pod *corev1.Pod) {
	podKey := client.ObjectKeyFromObject(pod)
	podDir := compute.PodRuntimeDir(podKey)

	/*---------------------------------------------------
	 * Generic Handler for ContainerStatus
	 *---------------------------------------------------*/
	handleStatus := func(containerStatus *corev1.ContainerStatus) {
		if containerStatus.RestartCount > 0 {
			panic("Restart is not yet supported")
		}

		/*-- Presence of Exit Code indicates Terminated  State--*/
		exitCodePath := podDir.Container(containerStatus.Name).ExitCodePath()
		exitCode, exitCodeExists := readIntFromFile(exitCodePath)

		if exitCodeExists {
			containerStatus.State.Waiting = nil
			containerStatus.State.Running = nil
			containerStatus.State.Terminated = &corev1.ContainerStateTerminated{
				ExitCode: int32(exitCode),
				Signal:   0,
				Reason: func() string {
					if exitCode == 0 {
						return "Success"
					} else {
						return "Error(" + containerStatus.Name + ")"
					}
				}(),
				Message: HumanReadableCode(exitCode),
				StartedAt: func() metav1.Time {
					if containerStatus.State.Running != nil {
						return containerStatus.State.Running.StartedAt
					} else {
						return metav1.Time{}
					}
				}(),
				FinishedAt:  metav1.Now(), // fixme: get it from the file's ctime
				ContainerID: containerStatus.ContainerID,
			}

			containerStatus.LastTerminationState = containerStatus.State

			return
		}

		jobIDPath := podDir.Container(containerStatus.Name).IDPath()
		jobID, jobIDExists := readStringFromFile(jobIDPath)

		/*-- Presence of Job ID indicated Running state (need to be set only once)--*/
		if jobIDExists {
			if containerStatus.State.Running == nil {
				job.SetContainerStatusID(containerStatus, jobID)

				containerStatus.State.Waiting = nil
				containerStatus.State.Running = &corev1.ContainerStateRunning{
					StartedAt: metav1.Now(), // fixme: we should get this info from the file's ctime
				}
				containerStatus.State.Terminated = nil

				/*-- todo: since we do not support probes, make everything to look ok --*/
				started := true
				containerStatus.Started = &started
				containerStatus.Ready = true
			}

			return
		}

		/*-- Lack of jobID indicates Waiting state --*/
		containerStatus.State.Waiting = &corev1.ContainerStateWaiting{
			Reason:  "InSlurmQueue",
			Message: "Job waiting in the Slurm queue",
		}
		containerStatus.State.Running = nil
		containerStatus.State.Terminated = nil
	}

	/*---------------------------------------------------
	 * Iterate containers and call the Generic Handler
	 *---------------------------------------------------*/
	for i := 0; i < len(pod.Status.InitContainerStatuses); i++ {
		handleStatus(&pod.Status.InitContainerStatuses[i])
	}

	for i := 0; i < len(pod.Status.ContainerStatuses); i++ {
		handleStatus(&pod.Status.ContainerStatuses[i])
	}
}
