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
	"github.com/carv-ics-forth/hpk/compute/slurm/apptainer"
	"github.com/carv-ics-forth/hpk/compute/slurm/job"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// buildContainer replicates the behavior of
// https://github.com/kubernetes/kubernetes/blob/master/pkg/kubelet/kuberuntime/kuberuntime_container.go
func (h *podHandler) buildContainer(container *corev1.Container, containerStatus *corev1.ContainerStatus) Container {
	/*---------------------------------------------------
	 * Generate Environment Variables
	 *---------------------------------------------------*/
	envFileTemplate, err := template.New(h.Name).
		Funcs(sprig.TxtFuncMap()).
		Option("missingkey=error").Parse(GenerateEnvTemplate)
	if err != nil {
		compute.SystemError(err, "generate env template error")
	}

	fields := GenerateEnvFields{
		Variables: append(h.podEnvVariables, container.Env...),
	}

	envFileContent := strings.Builder{}

	if err := envFileTemplate.Execute(&envFileContent, fields); err != nil {
		/*-- since both the template and fields are internal to the code, the evaluation should always succeed	--*/
		compute.SystemError(err, "failed to evaluate sbatch template")
	}

	envfilePath := h.podDirectory.Container(container.Name).EnvFilePath()

	if err := os.WriteFile(envfilePath, []byte(envFileContent.String()), compute.PodGlobalDirectoryPermissions); err != nil {
		compute.SystemError(err, "cannot write env file for container '%s' of pod '%s'", container, h.podKey)
	}

	/*---------------------------------------------------
	 * Prepare Mountpoints
	 *---------------------------------------------------*/
	binds := make([]string, len(container.VolumeMounts))

	for i, mount := range container.VolumeMounts {
		accessMode := func() string {
			if mount.ReadOnly {
				return ":ro"
			} else {
				return ":rw"
			}
		}

		// When subpath is zero, the scheme is: ".hpk/namespace/podName/.virtualenv/mountName:mountPath"
		// When subpath is non-zero, the scheme is ".hpk/namespace/podName/.virtualenv/mountName/subpath:mountPath"
		if mount.SubPath == "" {
			pathFile := filepath.Join(h.podDirectory.VolumeDir(), mount.Name)

			binds[i] = pathFile + ":" + mount.MountPath + accessMode()
		} else {
			subPathFile := filepath.Join(h.podDirectory.VolumeDir(), mount.Name, mount.SubPath)

			_, err := os.Stat(subPathFile)
			switch {
			case err == nil:
				binds[i] = subPathFile + ":" + mount.MountPath + accessMode()
			case os.IsNotExist(err):
				// If the file doesn't exist, create a dummy placeholder
				// FIXME: this can a security issue
				_, err := os.Create(subPathFile)
				if err != nil {
					compute.SystemError(err, "failed to create placeholder. subpath:'%s'", subPathFile)
				}
			default:
				compute.SystemError(err, "volume mounting has failed. mount:'%v'", mount)
			}
		}
	}

	/*---------------------------------------------------
	 * Prepare Container Paths
	 *---------------------------------------------------*/
	containerID := fmt.Sprintf("%s_%s_%s", h.Pod.GetNamespace(), h.Pod.GetName(), container.Name)

	imageID, _ := apptainer.PullImage(apptainer.Docker, container.Image)

	apptainerMode := func() string {
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
		ImageFilePath: imageID,
		Binds:         binds,
		Command:       container.Command,
		Args:          container.Args,
		ApptainerMode: apptainerMode,
		EnvFilePath:   containerPath.EnvFilePath(),
		JobIDPath:     containerPath.IDPath(),
		LogsPath:      containerPath.LogsPath(),
		ExitCodePath:  containerPath.ExitCodePath(),
	}

	/*---------------------------------------------------
	 * Update Container Status Fields
	 *---------------------------------------------------*/
	containerStatus.Name = container.Name
	containerStatus.ContainerID = containerID

	containerStatus.Image = container.Image
	containerStatus.ImageID = imageID

	return c
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
			logrus.Warnf("EXIT code:'%d' container:'%s'", exitCode, containerStatus.Name)

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
