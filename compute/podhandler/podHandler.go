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
	"bytes"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/carv-ics-forth/hpk/compute"
	"github.com/carv-ics-forth/hpk/compute/endpoint"
	"github.com/carv-ics-forth/hpk/compute/image"
	"github.com/carv-ics-forth/hpk/compute/runtime"
	"github.com/carv-ics-forth/hpk/compute/slurm"
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
	filePath := compute.HPK.Pod(podRef).EncodedJSONPath()

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
	filePath := compute.HPK.Pod(podRef).EncodedJSONPath()

	podDef, err := json.Marshal(pod)
	if err != nil {
		return errors.Wrapf(err, "failed encoding pod")
	}

	if err := os.WriteFile(filePath, podDef, endpoint.PodSpecJsonFilePermissions); err != nil {
		compute.SystemPanic(err, "failed to write file path '%s'", filePath)
	}

	return nil
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

	localPod, err := LoadPodFromKey(podKey)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// This behavior may raise when trying to delete a deleted pod.
			// However, deleting a pod from the fs does not guarantee deletion from Slurm.
			// For this reason, we just need to continue.
			return true
		}

		compute.SystemPanic(err, "failed to load pod")
	}

	/*---------------------------------------------------
	 * Cancel Slurm Job
	 *---------------------------------------------------*/
	if slurm.HasJobID(localPod) {
		jodID := slurm.GetJobID(localPod)

		out, err := slurm.CancelJob(jodID)
		if err != nil {
			if errors.Is(err, slurm.ErrInvalidJob) {
				logger.Info(" * No such Slurm job", "job", jodID, "pod", podKey)

				// the job does not exist, so it can be considered as deleted.
				goto remove_pod
			}

			if errors.Is(err, slurm.ErrRety) {
				logger.Info(" * Slurm job cannot be deleted. Retry later", "job", jodID, "pod", podKey, "out", out)

				return false
			}

			compute.SystemPanic(err, "failed to cancel job '%s' (%s). out: '%s'", jodID, podKey, out)
		}

		logger.Info(" * Slurm job is cancelled", "job", jodID, "pod", podKey, "out", out)
	}

	/*---------------------------------------------------
	 * Remove watcher for Pod Directory
	 *---------------------------------------------------*/
remove_pod:
	podDir := compute.HPK.Pod(podKey)

	// because fswatch does not work recursively, we cannot have the container directories nested within the pod.
	// instead, we use a flat directory in the format "podir/containername.{jid,stdout,stdour,...}"
	if err := watcher.Remove(podDir.String()); err != nil {
		compute.SystemPanic(err, "deregister watcher for path '%s' has failed", podDir)
	}

	logger.Info(" * Pod Watcher has been removed.")

	/*---------------------------------------------------
	 * Remove Pod Directory
	 *---------------------------------------------------*/

	if err := os.RemoveAll(podDir.String()); err != nil {
		// if trying to remove directory from the host fails, try to delete it using a fakeroot containera.
		if errors.Is(err, fs.ErrPermission) {
			compute.DefaultLogger.Info(" * Failed to remove directory from host. Try using fakeroot container.",
				"err", err,
			)

			// try to delete directory using the fakeroot from pause container.
			out, err := runtime.DefaultPauseImage.FakerootExec(
				[]string{"--mount", "type=bind,src=" + podDir.String() + ",dst=/pod"}, // mount the pod directory in singularity
				[]string{"rm", "-rf", "/pod/*"},                                       // remove the pod directory using fakeroot
			)

			compute.DefaultLogger.Info(" * Result",
				"out", out,
				"debug", []string{"-B", podDir.String() + ":" + podDir.String() + ":rw"},
			)

			if err != nil {
				compute.SystemPanic(err, "failed to forcible remove pod directory '%s'", podDir)
			}
		} else {
			compute.SystemPanic(err, "failed to remove pod directory '%s'", podDir)
		}
	}

	logger.Info(" * Pod directory is removed")

	/*---------------------------------------------------
	 * Garbage Collect Namespace
	 *---------------------------------------------------*/
	namespaceDir := filepath.Dir(podDir.String())
	if empty, _ := endpoint.IsEmpty(namespaceDir); empty {
		_ = os.RemoveAll(namespaceDir)

		logger.Info(" * Namespace directory is removed")
	}

	return true
}

type podHandler struct {
	*corev1.Pod

	podKey client.ObjectKey

	podEnvVariables []corev1.EnvVar
	podDirectory    endpoint.PodPath

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
		podDirectory:    compute.HPK.Pod(podKey),
		logger:          logger,
		podEnvVariables: FromServices(ctx, pod.GetNamespace()),
	}

	// create directory for the job environment.
	if err := os.MkdirAll(h.podDirectory.JobDir(), endpoint.PodGlobalDirectoryPermissions); err != nil {
		compute.SystemPanic(err, "Cant create pod directory '%s'", h.podDirectory.JobDir())
	}

	// create directory for logs.
	if err := os.MkdirAll(h.podDirectory.LogDir(), endpoint.PodGlobalDirectoryPermissions); err != nil {
		compute.SystemPanic(err, "cannot create log directory '%s'", h.podDirectory.LogDir())
	}

	// create directory for volumes.
	if err := os.MkdirAll(h.podDirectory.VolumeDir(), endpoint.PodGlobalDirectoryPermissions); err != nil {
		compute.SystemPanic(err, "cannot create volume directory '%s'", h.podDirectory.VolumeDir())
	}

	// create directory for control files.
	if err := os.MkdirAll(h.podDirectory.ControlFileDir(), endpoint.PodGlobalDirectoryPermissions); err != nil {
		compute.SystemPanic(err, "cannot create control file directory '%s'", h.podDirectory.ControlFileDir())
	}

	// watch for control files on the root directory of the pod.
	// because fswatch does not work recursively, we cannot have the container directories nested within the pod.
	// instead, we use a flat directory in the format "podir/containername.{jid,stdout,stdour,...}"
	if err := watcher.Add(h.podDirectory.ControlFileDir()); err != nil {
		if errors.Is(err, filenotify.ErrWatchExists) {
			logger.Info("Pod watcher already exists", "directory", h.podDirectory.ControlFileDir())
		} else {
			compute.SystemPanic(err, "register watcher for path '%s' has failed", h.podDirectory.ControlFileDir())
		}
	}

	logger.Info(" * Pod Environment has been created ")

	/*---------------------------------------------------
	 * Mount Volumes
	 *---------------------------------------------------*/
	for _, vol := range h.Pod.Spec.Volumes {
		// h.Pod.Spec.Containers[0].VolumeMounts
		if err := h.mountVolumeSource(ctx, vol); err != nil {
			compute.PodError(pod, "VolumeError", err.Error())

			return
		}
	}

	h.logger.Info(" * All volumes have been mounted")

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

	for _, container := range pod.Spec.Containers {
		resources.Sum(resourceRequest, container.Resources.Requests)
	}

	// create cgroups for the pod
	if compute.Environment.EnableCgroupV2 {
		if _, err := os.Create(h.podDirectory.CgroupFilePath()); err != nil {
			compute.SystemPanic(err, "Cant create cgroup configuration file '%s'", h.podDirectory.CgroupFilePath())
		}

		logger.Info(" * Cgroups are set")
	}

	/*---------------------------------------------------
	 * Prepare Image for Pause Container
	 *---------------------------------------------------*/
	pauseImage, err := image.Pull(compute.HPK.ImageDir(), image.Docker, image.PauseImage)
	if err != nil {
		compute.SystemPanic(err, "ImagePull error. Image:%s", image.PauseImage)
	}

	/*---------------------------------------------------
	 * Prepare Fields for Sbatch Templates
	 *---------------------------------------------------*/
	var customFlags []string
	if flags, hasFlags := h.Pod.GetAnnotations()[CustomSlurmFlags]; hasFlags {
		customFlags = strings.Split(flags, " ")
	}

	scriptTemplate, err := ParseTemplate(HostScriptTemplate)
	if err != nil {
		compute.SystemPanic(err, "sbatch template error")
	}

	scriptFileContent := bytes.Buffer{}

	if err := scriptTemplate.Execute(&scriptFileContent, JobFields{
		Pod:                h.podKey,
		PauseImageFilePath: pauseImage.Filepath,
		HostEnv:            compute.Environment,
		VirtualEnv: compute.VirtualEnvironment{
			PodDirectory:        h.podDirectory.String(),
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

	if err := os.WriteFile(scriptFilePath, scriptFileContent.Bytes(), endpoint.ContainerJobPermissions); err != nil {
		compute.SystemPanic(err, "unable to write sbatch script in file '%s'", scriptFilePath)
	}

	logger.Info(" * Slurm script has been generated")

	/*---------------------------------------------------
	 * Submit job to Slurm, and store the JobID
	 *---------------------------------------------------*/
	jobID, err := slurm.SubmitJob(scriptFilePath)
	if err != nil {
		compute.SystemPanic(err, "failed to submit job")
	}

	logger.Info(" * Slurm job has been submitted", "jobID", jobID)

	// update pod with the slurm's job id
	slurm.SetPodID(h.Pod, slurm.JobIDTypeSlurm, jobID)
	if err != nil {
		compute.SystemPanic(err, "failed to set job id for pod")
	}

	// needed for subsequent GetPod()
	if err := SavePodToFile(ctx, h.Pod); err != nil {
		compute.SystemPanic(err, "failed to persistent pod")
	}
}
