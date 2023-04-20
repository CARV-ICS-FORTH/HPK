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
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/Masterminds/sprig"
	"github.com/carv-ics-forth/hpk/compute"
	"github.com/carv-ics-forth/hpk/compute/slurm/job"
	"github.com/carv-ics-forth/hpk/pkg/filenotify"
	"github.com/carv-ics-forth/hpk/pkg/resources"
	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/json"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	CustomSlurmFlags = "slurm.hpk.io/flags"
)

// LoadPodFromDisk loads the CRD of a pod to the disk. The status may be driften, and call to SyncPodStatus is required.
func LoadPodFromDisk(podRef client.ObjectKey) *corev1.Pod {
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
		compute.SystemError(err, "failed to read pod description file '%s'", loadFrom)
	}

	var pod corev1.Pod

	if err := json.Unmarshal(encodedPod, &pod); err != nil {
		compute.SystemError(err, "cannot decode pod description file '%s'", loadFrom)
	}

	return &pod
}

func SavePodToDisk(_ context.Context, pod *corev1.Pod) {
	podKey := client.ObjectKeyFromObject(pod)
	podPath := compute.PodRuntimeDir(podKey)
	logger := compute.DefaultLogger.WithValues("pod", podKey)

	encodedPod, err := json.Marshal(pod)
	if err != nil {
		compute.SystemError(err, "cannot marshall pod '%s' to json", podKey)
	}

	if err := os.WriteFile(podPath.EncodedJSONPath(), encodedPod, compute.PodSpecJsonFilePermissions); err != nil {
		compute.SystemError(err, "cannot write pod json file '%s'", podPath.EncodedJSONPath())
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
			compute.SystemError(err, "Pod traversal error")
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

	localPod := LoadPodFromDisk(podKey)
	if localPod == nil {
		logger.Info("[WARN]: Tried to delete pod, but it does not exist in the cluster")

		return false
	}

	/*---------------------------------------------------
	 * Cancel Slurm Job
	 *---------------------------------------------------*/
	idType, podID := job.ParsePodID(localPod)

	if idType != job.JobIDTypeEmpty {
		logger.Info(" * Cancelling Slurm Job", "job", podID)

		out, err := job.CancelJob(podID)
		if err != nil {
			compute.SystemError(err, "failed to cancel job '%s'. out: '%s'", podID, out)
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
		compute.SystemError(err, "deregister watcher for path '%s' has failed", podDir)
	}

	/*---------------------------------------------------
	 * Remove Pod Directory
	 *---------------------------------------------------*/
	logger.Info(" * Removing Pod Directory")

	if err := os.RemoveAll(string(podDir)); err != nil && !errors.Is(err, os.ErrNotExist) {
		compute.SystemError(err, "failed to delete pod directory %s'", podDir)
	}

	return true
}

type podHandler struct {
	*corev1.Pod

	podKey client.ObjectKey

	podEnvVariables []corev1.EnvVar
	podDirectory    compute.PodPath

	logger logr.Logger
}

func CreatePod(ctx context.Context, pod *corev1.Pod, watcher filenotify.FileWatcher) error {
	/*---------------------------------------------------
	 * Prepare the Pod Execution Environment
	 *---------------------------------------------------*/
	podKey := client.ObjectKeyFromObject(pod)
	logger := compute.DefaultLogger.WithValues("pod", podKey)

	h := podHandler{
		Pod:             pod,
		podKey:          podKey,
		podDirectory:    compute.PodRuntimeDir(podKey),
		logger:          logger,
		podEnvVariables: FromServices(ctx, pod.GetNamespace()),
	}

	logger.Info(" * Creating Pod Environment ")

	if err := os.MkdirAll(h.podDirectory.VirtualEnvironmentDir().String(), compute.PodGlobalDirectoryPermissions); err != nil {
		compute.SystemError(err, "Cant create pod directory '%s'", h.podDirectory.VirtualEnvironmentDir().String())
	}

	if err := os.MkdirAll(h.podDirectory.VolumeDir(), compute.PodGlobalDirectoryPermissions); err != nil {
		compute.SystemError(err, "Cant create volume directory '%s'", h.podDirectory.VirtualEnvironmentDir().String())
	}

	// TODO: add the pod limit's
	if _, err := os.Create(h.podDirectory.CgroupFilePath()); err != nil {
		compute.SystemError(err, "Cant create cgroup configuration file '%s'", h.podDirectory.CgroupFilePath())
	}

	h.logger.Info(" * Mounting Volumes")
	for _, vol := range h.Pod.Spec.Volumes {
		h.mountVolumeSource(ctx, vol)
	}

	h.logger.Info(" * Register Watchers for Pod Directory")
	// because fswatch does not work recursively, we cannot have the container directories nested within the pod.
	// instead, we use a flat directory in the format "podir/containername.{jid,stdout,stdour,...}"
	if err := watcher.Add(string(h.podDirectory)); err != nil {
		compute.SystemError(err, "register watcher for path '%s' has failed", h.podDirectory)
	}

	/*---------------------------------------------------
	 * Build Container Commands
	 *---------------------------------------------------*/
	// podResourceLimits := resources.NewResourceList()
	resourceRequest := resources.NewResourceList()

	var initContainers []Container
	pod.Status.InitContainerStatuses = make([]corev1.ContainerStatus, len(pod.Spec.InitContainers))

	for i := range pod.Spec.InitContainers {
		initContainer := &pod.Spec.InitContainers[i]
		initContainerStatus := &pod.Status.InitContainerStatuses[i]

		initContainers = append(initContainers, h.buildContainer(initContainer, initContainerStatus))

		//	resources.Sum(podResourceLimits, initContainer.Resources.Limits)
		resources.Sum(resourceRequest, initContainer.Resources.Requests)
	}

	var containers []Container
	pod.Status.ContainerStatuses = make([]corev1.ContainerStatus, len(pod.Spec.Containers))

	for i := range pod.Spec.Containers {
		container := &pod.Spec.Containers[i]
		containerStatus := &pod.Status.ContainerStatuses[i]

		containers = append(containers, h.buildContainer(container, containerStatus))

		//	resources.Sum(podResourceLimits, container.Resources.Limits)
		resources.Sum(resourceRequest, container.Resources.Requests)
	}

	/*---------------------------------------------------
	 * Prepare Fields for Sbatch Templates
	 *---------------------------------------------------*/
	logger.Info(" * Preparing job submission script")

	// add custom user flags
	var customFlags []string
	if flags, hasFlags := h.Pod.GetAnnotations()[CustomSlurmFlags]; hasFlags {
		customFlags = strings.Split(flags, " ")
	}

	scriptTemplate, err := template.New(h.Name).
		Funcs(sprig.TxtFuncMap()).
		Option("missingkey=error").Parse(SbatchScriptTemplate)
	if err != nil {
		/*-- template errors should be expected from the custom fields where users can inject shitty input.	--*/
		compute.SystemError(err, "sbatch template error")
	}

	scriptFileContent := bytes.Buffer{}

	if err := scriptTemplate.Execute(&scriptFileContent, JobFields{
		Pod:        h.podKey,
		ComputeEnv: compute.Environment,
		VirtualEnv: VirtualEnvironmentPaths{
			CgroupFilePath:      h.podDirectory.CgroupFilePath(),
			ConstructorFilePath: h.podDirectory.ConstructorFilePath(),
			IPAddressPath:       h.podDirectory.IPAddressPath(),
			StdoutPath:          h.podDirectory.StdoutPath(),
			StderrPath:          h.podDirectory.StderrPath(),
			SysErrorPath:        h.podDirectory.SysErrorPath(),
		},
		InitContainers:  initContainers,
		Containers:      containers,
		ResourceRequest: resources.ResourceListToStruct(resourceRequest),
		CustomFlags:     customFlags,
	}); err != nil {
		/*-- since both the template and fields are internal to the code, the evaluation should always succeed	--*/
		compute.SystemError(err, "failed to evaluate sbatch template")
	}

	scriptFilePath := h.podDirectory.SubmitJobPath()

	if err := os.WriteFile(scriptFilePath, scriptFileContent.Bytes(), compute.ContainerJobPermissions); err != nil {
		compute.SystemError(err, "unable to write sbatch script in file '%s'", scriptFilePath)
	}

	/*---------------------------------------------------
	 * Submit job to Slurm, and store the JobID
	 *---------------------------------------------------*/
	logger.Info(" * Submitting sbatch to Slurm")

	jobID, err := job.SubmitJob(scriptFilePath)
	if err != nil {
		compute.SystemError(err, "failed to submit job")
	}

	h.logger.Info(" * Setting Slurm Job ID to Pod ", "jobID", jobID)

	/*-- Update Pod with the JobID --*/
	job.SetPodID(h.Pod, job.JobIDTypeSlurm, jobID)
	if err != nil {
		compute.SystemError(err, "failed to set job id for pod")
	}

	/*-- Needed as it will follow a GetPod()--*/
	SavePodToDisk(ctx, h.Pod)

	return nil
}
