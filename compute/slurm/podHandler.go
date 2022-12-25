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
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/Masterminds/sprig"
	"github.com/carv-ics-forth/hpk/compute"
	"github.com/carv-ics-forth/hpk/pkg/filenotify"
	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/json"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type podHandler struct {
	*corev1.Pod

	podKey client.ObjectKey

	podEnvVariables         []corev1.EnvVar
	podDirectory            compute.PodPath
	internalPodDirectory    compute.PodPath

	// podMountSymlinks is used to bypass the default mounting behavior.
	// instead of mounting directory from the podDirectory/volname:/containerpath,
	// this will mount hostpath/containerpath
	podMountSymlinks map[string]string

	logger logr.Logger
}

func LoadPod(podRef client.ObjectKey) *corev1.Pod {
	loadFrom := compute.PodRuntimeDir(podRef).EncodedJSONPath()

	encodedPod, err := os.ReadFile(loadFrom)
	if os.IsNotExist(err) {
		/*
				if err not found, it means that:
			 	1) the pod is  not scheduled on this node (user's problem).
				2) someone removed the file (administrator's problem).
				3) we have a race condition (our problem).
		*/
		return nil
	} else if err != nil {
		SystemError(err, "failed to read pod description file '%s'", loadFrom)
	}

	var pod corev1.Pod

	if err := json.Unmarshal(encodedPod, &pod); err != nil {
		SystemError(err, "cannot decode pod description file '%s'", loadFrom)
	}

	return &pod
}

func SavePod(_ context.Context, pod *corev1.Pod) {
	podKey := client.ObjectKeyFromObject(pod)
	logger := compute.DefaultLogger.WithValues("pod", podKey)

	encodedPod, err := json.Marshal(pod)
	if err != nil {
		SystemError(err, "cannot marshall pod '%s' to json", podKey)
	}

	saveTo := compute.PodRuntimeDir(podKey).EncodedJSONPath()

	if err := os.WriteFile(saveTo, encodedPod, compute.PodSpecJsonFilePermissions); err != nil {
		SystemError(err, "cannot write pod json file '%s'", saveTo)
	}

	logger.Info("** Local Status Updated ** ",
		"version", pod.ResourceVersion,
		"phase", pod.Status.Phase,
	)
}

func WalkPodDirectories(f compute.WalkPodFunc) error {
	rootDir := compute.RuntimeDir
	maxDepth := strings.Count(rootDir, string(os.PathSeparator)) + 2 // expect path .hpk/namespace/pod

	return filepath.WalkDir(rootDir, func(path string, info os.DirEntry, err error) error {
		if err != nil {
			SystemError(err, "Pod traversal error")
		}

		// account only paths in the form ~/.hpk/namespace/pod
		if info.IsDir() && strings.Count(path, string(os.PathSeparator)) == maxDepth {
			return f(compute.PodPath(path))
		}

		return nil
	})
}

/*
DeletePod takes a Pod Reference and deletes the Pod from the provider.
DeletePod may be called multiple times for the same pod.

Notice that by using the reference, we operate on the local copy instead of the remote. This serves two purposes:
1) We can extract updated information from .spec (Kubernetes only fetches .Status)
2) We can have "fresh" information that is not yet propagated to Kubernetes
*/
func DeletePod(podKey client.ObjectKey, watcher filenotify.FileWatcher) bool {
	logger := compute.DefaultLogger.WithValues("pod", podKey)

	podDir := compute.PodRuntimeDir(podKey)

	localPod := LoadPod(podKey)
	if localPod == nil {
		logger.Info("[WARN]: Tried to delete pod, but it does not exist in the cluster")

		return false
	}

	/*---------------------------------------------------
	 * Cancel Slurm Job
	 *---------------------------------------------------*/
	idType, podID := ParsePodID(localPod)

	if idType != JobIDTypeEmpty {
		logger.Info(" * Cancelling Slurm Job", "job", podID)

		out, err := CancelJob(podID)
		if err != nil {
			SystemError(err, "failed to cancel job '%s'. out: '%s'", podID, out)
		}
	} else {
		logger.Info(" * No Slurm ID was found.")
	}

	/*---------------------------------------------------
	 * Remove watcher for Pod Directory
	 *---------------------------------------------------*/
	// because fswatch does not work recursively, we cannot have the container directories nested within the pod.
	// instead, we use a flat directory in the format "podir/containername.{jid,stdout,stdour,...}"
	if err := watcher.Remove(string(podDir)); err != nil {
		SystemError(err, "deregister watcher for path '%s' has failed", podDir)
	}

	/*---------------------------------------------------
	 * Remove Pod Directory
	 *---------------------------------------------------*/
	logger.Info(" * Removing Pod Directory")

	if err := os.RemoveAll(string(podDir)); err != nil && !errors.Is(err, os.ErrNotExist) {
		SystemError(err, "failed to delete pod directory %s'", podDir)
	}

	return true
}

func CreatePod(ctx context.Context, pod *corev1.Pod, watcher filenotify.FileWatcher) error {
	podKey := client.ObjectKeyFromObject(pod)
	logger := compute.DefaultLogger.WithValues("pod", podKey)

	/*---------------------------------------------------
	 * Pre-Populate Container.Status from Container.Spec
	 *---------------------------------------------------*/
	pod.Status.InitContainerStatuses = make([]corev1.ContainerStatus, len(pod.Spec.InitContainers))
	for i := 0; i < len(pod.Spec.InitContainers); i++ {
		pod.Status.InitContainerStatuses[i].Name = pod.Spec.InitContainers[i].Name
		pod.Status.InitContainerStatuses[i].Image = pod.Spec.InitContainers[i].Image
	}

	pod.Status.ContainerStatuses = make([]corev1.ContainerStatus, len(pod.Spec.Containers))
	pod.Status.ContainerStatuses = make([]corev1.ContainerStatus, len(pod.Spec.Containers))
	for i := 0; i < len(pod.Spec.Containers); i++ {
		pod.Status.ContainerStatuses[i].Name = pod.Spec.Containers[i].Name
		pod.Status.ContainerStatuses[i].Image = pod.Spec.Containers[i].Image
	}

	/*---------------------------------------------------
	 * Create the Pod directory and Pod Handlers
	 *---------------------------------------------------*/
	logger.Info(" * Creating Host Directory and Pod Handler")

	virtualEnvironmentDir := compute.PodRuntimeDir(podKey).VirtualEnvironmentDir()

	if err := os.MkdirAll(string(virtualEnvironmentDir), compute.PodGlobalDirectoryPermissions); err != nil {
		SystemError(err, "Cant create pod directory '%s'", virtualEnvironmentDir)
	}

	h := podHandler{
		Pod:                  pod,
		podKey:               podKey,
		podDirectory:         compute.PodRuntimeDir(podKey),
		internalPodDirectory: compute.InternalPodRuntimeDir(podKey),
		podMountSymlinks:     map[string]string{},
		logger:               logger,
	}

	/*---------------------------------------------------
	 * Prepare the Pod environment
	 *---------------------------------------------------*/
	logger.Info(" * Creating Environment Variables")
	h.podEnvVariables = FromServices(ctx, pod.GetNamespace())

	h.logger.Info(" * Mounting Volumes")
	h.prepareVolumes(ctx)

	logger.Info(" * Listening for async changes on host directory")
	// because fswatch does not work recursively, we cannot have the container directories nested within the pod.
	// instead, we use a flat directory in the format "podir/containername.{jid,stdout,stdour,...}"
	if err := watcher.Add(string(h.podDirectory)); err != nil {
		SystemError(err, "register watcher for path '%s' has failed", h.podDirectory)
	}

	/*---------------------------------------------------
	 * Build Container Commands
	 *---------------------------------------------------*/
	var initContainers []Container

	for i := range pod.Spec.InitContainers {
		initContainers = append(initContainers, h.buildContainer(&pod.Spec.InitContainers[i]))
	}

	var containers []Container

	for i := range pod.Spec.Containers {
		containers = append(containers, h.buildContainer(&pod.Spec.Containers[i]))
	}

	/*---------------------------------------------------
	 * Prepare Fields for Sbatch Templates
	 *---------------------------------------------------*/
	logger.Info(" * Preparing job submission script")

	scriptFilePath := h.podDirectory.SubmitJobPath()
	scriptFileContent := strings.Builder{}

	scriptTemplate, err := template.New(h.Name).
		Funcs(sprig.TxtFuncMap()).
		Option("missingkey=error").Parse(SbatchScriptTemplate)
	if err != nil {
		/*-- template errors should be expected from the custom fields where users can inject shitty input.	--*/
		SystemError(err, "sbatch template error")
	}

	if err := scriptTemplate.Execute(&scriptFileContent, SbatchScriptFields{
		Pod:        h.podKey,
		ComputeEnv: compute.Environment,
		VirtualEnv: VirtualEnvironmentPaths{
			ConstructorPath: h.internalPodDirectory.ConstructorPath(),
			IPAddressPath:   h.internalPodDirectory.IPAddressPath(),
			StdoutPath:      h.internalPodDirectory.StdoutPath(),
			StderrPath:      h.internalPodDirectory.StderrPath(),
			SysErrorPath:    h.internalPodDirectory.SysErrorPath(),
		},
		InitContainers: initContainers,
		Containers:     containers,
		Options:        RequestOptions{},
	}); err != nil {
		/*-- since both the template and fields are internal to the code, the evaluation should always succeed	--*/
		SystemError(err, "failed to evaluate sbatch template")
	}

	if err := os.WriteFile(scriptFilePath, []byte(scriptFileContent.String()), compute.ContainerJobPermissions); err != nil {
		SystemError(err, "unable to write sbatch script in file '%s'", scriptFilePath)
	}

	/*---------------------------------------------------
	 * Submit job to Slurm, and store the JobID
	 *---------------------------------------------------*/
	logger.Info(" * Submitting sbatch to Slurm")

	jobID, err := SubmitJob(scriptFilePath)
	if err != nil {
		SystemError(err, "failed to submit job")
	}

	h.logger.Info(" * Setting Slurm Job ID to Pod ", "jobID", jobID)

	/*-- Update Pod with the JobID --*/
	SetPodID(h.Pod, JobIDTypeSlurm, jobID)
	if err != nil {
		SystemError(err, "failed to set job id for pod")
	}

	/*-- Needed as it will follow a GetPod()--*/
	SavePod(ctx, h.Pod)

	return nil
}

// buildContainer replicates the behavior of
// https://github.com/kubernetes/kubernetes/blob/master/pkg/kubelet/kuberuntime/kuberuntime_container.go
func (h *podHandler) buildContainer(container *corev1.Container) Container {
	/*---------------------------------------------------
	 * Generate Environment Variables
	 *---------------------------------------------------*/
	envfilePath := h.podDirectory.Container(container.Name).EnvFilePath()
	envFileContent := strings.Builder{}

	envFileTemplate, err := template.New(h.Name).
		Funcs(sprig.TxtFuncMap()).
		Option("missingkey=error").Parse(GenerateEnvTemplate)
	if err != nil {
		SystemError(err, "generate env template error")
	}

	if err := envFileTemplate.Execute(&envFileContent, GenerateEnvFields{
		Variables: append(h.podEnvVariables, container.Env...),
	}); err != nil {
		/*-- since both the template and fields are internal to the code, the evaluation should always succeed	--*/
		SystemError(err, "failed to evaluate sbatch template")
	}

	if err := os.WriteFile(envfilePath, []byte(envFileContent.String()), compute.PodGlobalDirectoryPermissions); err != nil {
		SystemError(err, "cannot write env file for container '%s' of pod '%s'", container, h.podKey)
	}

	/*---------------------------------------------------
	 * Prepare fields for Container Template
	 *---------------------------------------------------*/
	containerPath := h.internalPodDirectory.Container(container.Name)

	return Container{
		InstanceName: fmt.Sprintf("%s_%s_%s", h.Pod.GetNamespace(), h.Pod.GetName(), container.Name),
		Image:        compute.Environment.ContainerRegistry + container.Image,
		EnvFilePath:  envfilePath,
		Binds: func() []string {
			mountArgs := make([]string, 0, len(container.VolumeMounts))

			for _, mountVar := range container.VolumeMounts {
				hostpath, isSymlink := h.podMountSymlinks[mountVar.Name]
				if isSymlink {
					h.logger.Info("Bind volume as symlink",
						"host", hostpath,
						"container", mountVar.MountPath,
					)

					mountArgs = append(mountArgs, hostpath+":"+mountVar.MountPath)
				} else {
					mountArgs = append(mountArgs, h.internalPodDirectory.Mountpaths(mountVar))
				}
			}

			return mountArgs
		}(),
		Command: container.Command,
		Args:    container.Args,
		ApptainerMode: func() string {
			if container.Command == nil {
				return "run"
			} else {
				return "exec"
			}
		}(),
		JobIDPath:    containerPath.IDPath(),
		LogsPath:     containerPath.LogsPath(),
		ExitCodePath: containerPath.ExitCodePath(),
	}
}
