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
	"io/fs"
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

// LoadPodFromKey waits LoadPodFromFile with filePath discovery.
func LoadPodFromKey(podRef client.ObjectKey) (*corev1.Pod, error) {
	filePath := compute.PodRuntimeDir(podRef).EncodedJSONPath()

	return LoadPodFromFile(filePath)
}

// LoadPodFromFile will read, decode, and return a Pod from a file.
func LoadPodFromFile(filePath string) (*corev1.Pod, error) {
	if filePath == "" {
		return nil, errors.Errorf("file path not specified")
	}

	podDef, err := os.ReadFile(filePath)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to read file path %s", filePath)
	}

	if len(podDef) == 0 {
		return nil, errors.Errorf("file was empty: %s", filePath)
	}

	var pod corev1.Pod

	if err := json.Unmarshal(podDef, &pod); err != nil {
		compute.SystemPanic(err, "failed decoding file '%s'", filePath)
	}

	return &pod, nil
}

func SavePodToFile(_ context.Context, pod *corev1.Pod) error {
	if pod == nil {
		return errors.Errorf("empty pod")
	}

	podRef := client.ObjectKeyFromObject(pod)
	filePath := compute.PodRuntimeDir(podRef).EncodedJSONPath()

	podDef, err := json.Marshal(pod)
	if err != nil {
		return errors.Wrapf(err, "failed encoding pod")
	}

	if err := os.WriteFile(filePath, podDef, compute.PodSpecJsonFilePermissions); err != nil {
		compute.SystemPanic(err, "failed to write file path '%s'", filePath)
	}

	return nil
}

func WalkPodDirectories(f compute.WalkPodFunc) error {
	rootDir := compute.RuntimeDir
	maxDepth := strings.Count(rootDir, string(os.PathSeparator)) + 2 // expect path .hpk/namespace/pod

	return filepath.WalkDir(rootDir, func(path string, info os.DirEntry, err error) error {
		if err != nil {
			compute.SystemPanic(err, "Pod traversal error")
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

	localPod, err := LoadPodFromKey(podKey)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			logger.Info("Pod marked for deletion but it does not exist.")

			// This behavior may raise when trying to delete a deleted pod.
			// So for idempotency, we return the Pod as being (now) deleted.
			// However, it may be possible to leave rogue jobs running on slurm.
			return true
		}

		compute.SystemPanic(err, "failed to load pod")
	}

	/*---------------------------------------------------
	 * Cancel Slurm Job
	 *---------------------------------------------------*/
	idType, podID := job.ParsePodID(localPod)

	if idType != job.JobIDTypeEmpty {
		out, err := job.CancelJob(podID)
		if err != nil {
			compute.SystemPanic(err, "failed to cancel job '%s'. out: '%s'", podID, out)
		}

		logger.Info(" * Slurm job is cancelled", "job", podID)
	} else {
		logger.Info(" * No Slurm ID was found.")
	}

	/*---------------------------------------------------
	 * Remove watcher for Pod Directory
	 *---------------------------------------------------*/
	// because fswatch does not work recursively, we cannot have the container directories nested within the pod.
	// instead, we use a flat directory in the format "podir/containername.{jid,stdout,stdour,...}"
	if err := watcher.Remove(podDir.String()); err != nil {
		compute.SystemPanic(err, "deregister watcher for path '%s' has failed", podDir)
	}

	/*---------------------------------------------------
	 * Remove Pod Directory
	 *---------------------------------------------------*/
	if err := os.RemoveAll(podDir.String()); err != nil && !errors.Is(err, os.ErrNotExist) {
		compute.SystemPanic(err, "failed to delete pod directory %s'", podDir)
	}

	logger.Info(" * Pod directory is removed")

	return true
}

type podHandler struct {
	*corev1.Pod

	podKey client.ObjectKey

	podEnvVariables []corev1.EnvVar
	podDirectory    compute.PodPath

	logger logr.Logger
}

func CreatePod(ctx context.Context, pod *corev1.Pod, watcher filenotify.FileWatcher) {
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

	if err := os.MkdirAll(h.podDirectory.VirtualEnvironmentDir().String(), compute.PodGlobalDirectoryPermissions); err != nil {
		compute.SystemPanic(err, "Cant create pod directory '%s'", h.podDirectory.VirtualEnvironmentDir().String())
	}

	// because fswatch does not work recursively, we cannot have the container directories nested within the pod.
	// instead, we use a flat directory in the format "podir/containername.{jid,stdout,stdour,...}"
	if err := watcher.Add(string(h.podDirectory)); err != nil {
		compute.SystemPanic(err, "register watcher for path '%s' has failed", h.podDirectory)
	}

	logger.Info(" * Pod Environment has been created ")

	/*---------------------------------------------------
	 * Prepare Pod Volumes
	 *---------------------------------------------------*/
	if err := os.MkdirAll(h.podDirectory.VolumeDir(), compute.PodGlobalDirectoryPermissions); err != nil {
		compute.SystemPanic(err, "Cant create volume directory '%s'", h.podDirectory.VirtualEnvironmentDir().String())
	}

	for _, vol := range h.Pod.Spec.Volumes {
		h.mountVolumeSource(ctx, vol)
	}

	h.logger.Info(" * All volumes are mounted")

	/*---------------------------------------------------
	 * Build Container Commands
	 *---------------------------------------------------*/
	var initContainers []Container
	pod.Status.InitContainerStatuses = make([]corev1.ContainerStatus, len(pod.Spec.InitContainers))

	for i := range pod.Spec.InitContainers {
		initContainer := &pod.Spec.InitContainers[i]
		initContainerStatus := &pod.Status.InitContainerStatuses[i]

		c, err := h.buildContainer(initContainer, initContainerStatus)
		if err != nil {
			compute.PodError(pod, "InitContainerError", "failed to materialize pod.Spec.InitContainers[%d]", i)

			return
		}

		initContainers = append(initContainers, c)
	}

	var containers []Container
	pod.Status.ContainerStatuses = make([]corev1.ContainerStatus, len(pod.Spec.Containers))

	for i := range pod.Spec.Containers {
		container := &pod.Spec.Containers[i]
		containerStatus := &pod.Status.ContainerStatuses[i]

		c, err := h.buildContainer(container, containerStatus)
		if err != nil {
			compute.PodError(pod, "InitContainerError", "failed to materialize pod.Spec.Containers[%d]", i)

			return
		}

		containers = append(containers, c)
	}

	/*---------------------------------------------------
	 * Handle Cgroups and Resource Reservation
	 *---------------------------------------------------*/
	resourceRequest := resources.NewResourceList()

	// set per-container limitations
	// TODO: add the pod limit's
	for _, initContainer := range pod.Spec.InitContainers {
		resources.Sum(resourceRequest, initContainer.Resources.Requests)
	}

	for _, container := range pod.Spec.InitContainers {
		resources.Sum(resourceRequest, container.Resources.Requests)
	}

	// create cgroups for the pod
	if _, err := os.Create(h.podDirectory.CgroupFilePath()); err != nil {
		compute.SystemPanic(err, "Cant create cgroup configuration file '%s'", h.podDirectory.CgroupFilePath())
	}

	logger.Info(" * Cgroups are prepared")

	/*---------------------------------------------------
	 * Prepare Fields for Sbatch Templates
	 *---------------------------------------------------*/
	var customFlags []string
	if flags, hasFlags := h.Pod.GetAnnotations()[CustomSlurmFlags]; hasFlags {
		customFlags = strings.Split(flags, " ")
	}

	scriptTemplate, err := template.New(h.Name).
		Funcs(sprig.TxtFuncMap()).
		Option("missingkey=error").Parse(SlurmScriptTemplate)
	if err != nil {
		compute.SystemPanic(err, "sbatch template error")
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
			SysErrorFilePath:    h.podDirectory.SysErrorFilePath(),
		},
		InitContainers:  initContainers,
		Containers:      containers,
		ResourceRequest: resources.ResourceListToStruct(resourceRequest),
		CustomFlags:     customFlags,
	}); err != nil {
		/*-- since both the template and fields are internal to the code, the evaluation should always succeed	--*/
		compute.SystemPanic(err, "failed to evaluate sbatch template")
	}

	scriptFilePath := h.podDirectory.SubmitJobPath()

	if err := os.WriteFile(scriptFilePath, scriptFileContent.Bytes(), compute.ContainerJobPermissions); err != nil {
		compute.SystemPanic(err, "unable to write sbatch script in file '%s'", scriptFilePath)
	}

	logger.Info(" * Slurm script is ready")

	/*---------------------------------------------------
	 * Submit job to Slurm, and store the JobID
	 *---------------------------------------------------*/
	jobID, err := job.SubmitJob(scriptFilePath)
	if err != nil {
		compute.SystemPanic(err, "failed to submit job")
	}

	logger.Info(" * Slurm job has been submitted", "jobID", jobID)

	// update pod with the slurm's job id
	job.SetPodID(h.Pod, job.JobIDTypeSlurm, jobID)
	if err != nil {
		compute.SystemPanic(err, "failed to set job id for pod")
	}

	// needed for subsequent GetPod()
	if err := SavePodToFile(ctx, h.Pod); err != nil {
		compute.SystemPanic(err, "failed to persistent pod")
	}
}
