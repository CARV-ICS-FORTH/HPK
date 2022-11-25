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
	"github.com/fsnotify/fsnotify"
	"github.com/go-logr/logr"
	"github.com/hashicorp/go-multierror"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/json"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type podHandler struct {
	*corev1.Pod

	podKey client.ObjectKey

	podEnvVariables []corev1.EnvVar
	podDirectory    compute.PodPath

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

func GetPods() ([]*corev1.Pod, error) {
	var pods []*corev1.Pod
	var merr *multierror.Error

	/*---------------------------------------------------
	 * Iterate the filesystem and extract local pods
	 *---------------------------------------------------*/
	filepath.Walk(compute.RuntimeDir, func(path string, info os.FileInfo, err error) error {
		encodedPod, err := os.ReadFile(path)
		if err != nil {
			merr = multierror.Append(merr, errors.Wrapf(err, "cannot read pod description file '%s'", path))
		}

		var pod corev1.Pod

		if err := json.Unmarshal(encodedPod, &pod); err != nil {
			merr = multierror.Append(merr, errors.Wrapf(err, "cannot decode pod description file '%s'", path))
		}

		/*-- return only the pods that are known to be running --*/
		if pod.Status.Phase == corev1.PodRunning {
			pods = append(pods, &pod)
		}

		return nil
	})

	return pods, merr.ErrorOrNil()
}

/*
DeletePod takes a Pod Reference and deletes the Pod from the provider.
DeletePod may be called multiple times for the same pod.

Notice that by using the reference, we operate on the local copy instead of the remote. This serves two purposes:
1) We can extract updated information from .spec (Kubernetes only fetches .Status)
2) We can have "fresh" information that is not yet propagated to Kubernetes
*/
func DeletePod(podKey client.ObjectKey) bool {
	logger := compute.DefaultLogger.WithValues("pod", podKey)

	pod := LoadPod(podKey)
	if pod == nil {
		logger.Info("[WARN]: Tried to delete pod, but it dod not exist in the cluster")

		return false
	}

	/*---------------------------------------------------
	 * Cancel Slurm Job
	 *---------------------------------------------------*/
	idType, podID := ParsePodID(pod)

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
	 * Remove Pod Directory
	 *---------------------------------------------------*/
	logger.Info(" * Removing Pod Directory")

	podDir := compute.PodRuntimeDir(podKey)

	if err := os.RemoveAll(string(podDir)); err != nil && !errors.Is(err, os.ErrNotExist) {
		SystemError(err, "failed to delete pod directory %s'", podDir)
	}

	return true
}

func CreatePod(ctx context.Context, pod *corev1.Pod, watcher *fsnotify.Watcher) error {
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
	 * Create the shared directory of Virtual Environment
	 *---------------------------------------------------*/
	logger.Info("== Building New Virtual Environment ==")

	virtualEnvironmentDir := compute.PodRuntimeDir(podKey).VirtualEnvironmentDir()

	if err := os.MkdirAll(string(virtualEnvironmentDir), compute.PodGlobalDirectoryPermissions); err != nil {
		SystemError(err, "Cant create sbatch directory '%s'", virtualEnvironmentDir)
	}

	/*---------------------------------------------------
	 * Setting a list of Services the pod should see
	 *---------------------------------------------------*/

	/*
		podEnvVariables, err := getServiceEnvVarMap(ctx, pod.GetNamespace())
		if err != nil {
			SystemError(err, "Cant create environment variables")
		}

	*/
	var serviceList corev1.ServiceList

	if err := compute.K8SClient.List(ctx, &serviceList, &client.ListOptions{
		LabelSelector: labels.Everything(),
	}); err != nil {
		SystemError(err, "failed to list services when setting up env vars")
	}

	var slist []*corev1.Service

	for i := range serviceList.Items {
		slist = append(slist, &serviceList.Items[i])
	}

	podEnvVariables := FromServices(slist)

	/*---------------------------------------------------
	 * Set tha Pod Handler
	 *---------------------------------------------------*/
	h := podHandler{
		Pod:             pod,
		podKey:          podKey,
		podDirectory:    compute.PodRuntimeDir(podKey),
		podEnvVariables: podEnvVariables,
		logger:          logger,
	}

	/*-- Kind of Journaling --*/
	// SavePod(ctx, pod)

	/*---------------------------------------------------
	 * Prepare Volumes on the Pod
	 *---------------------------------------------------*/
	logger.Info(" * Creating Backend Volumes")

	h.prepareVolumes(ctx)

	/*---------------------------------------------------
	 * Set listeners for async changes on the Pod
	 *---------------------------------------------------*/
	logger.Info(" * Setting Filesystem Notifiers", "watchPath", h.podDirectory)

	// because fswatch does not work recursively, we cannot have the container directories nested within the pod.
	// instead, we use a flat directory in the format "podir/containername.{jid,stdout,stdour,...}"
	if err := watcher.Add(string(h.podDirectory)); err != nil {
		SystemError(err, "register to fsnotify has failed for path '%s'", h.podDirectory)
	}

	/*---------------------------------------------------
	 * Build Container Commands for Init Containers
	 *---------------------------------------------------*/
	logger.Info(" * Setting Apptainer for init containers")

	var initContainers []Container

	for i := range pod.Spec.InitContainers {
		initContainers = append(initContainers, h.buildContainer(&pod.Spec.InitContainers[i]))
	}

	/*---------------------------------------------------
	 * Build Container Commands for Containers
	 *---------------------------------------------------*/
	logger.Info(" * Setting Apptainer for containers")

	var containers []Container

	for i := range pod.Spec.Containers {
		containers = append(containers, h.buildContainer(&pod.Spec.Containers[i]))
	}

	/*---------------------------------------------------
	 * Prepare Fields for Sbatch Templates
	 *---------------------------------------------------*/
	logger.Info(" * Setting Fields for Sbatch Template")

	/*-- Set HPK-defined fields for Sbatch Template --*/
	fields := SbatchScriptFields{
		Pod:        h.podKey,
		ComputeEnv: compute.Environment,
		VirtualEnv: VirtualEnvironmentPaths{
			ConstructorPath: h.podDirectory.ConstructorPath(),
			JobIDPath:       h.podDirectory.IDPath(),
			IPAddressPath:   h.podDirectory.IPAddressPath(),
			StdoutPath:      h.podDirectory.StdoutPath(),
			StderrPath:      h.podDirectory.StderrPath(),
			ExitCodePath:    h.podDirectory.ExitCodePath(),
			SysErrorPath:    h.podDirectory.SysErrorPath(),
		},
		InitContainers: initContainers,
		Containers:     containers,
		Options:        RequestOptions{},
	}

	/*-- Set user-defined sbatch flags (e.g, From Argo) --*/
	if slurmFlags, ok := h.Pod.GetAnnotations()["slurm-job/flags"]; ok {
		for _, slurmFlag := range strings.Split(slurmFlags, " ") {
			fields.Options.CustomFlags = append(fields.Options.CustomFlags,
				"\n#SBATCH "+slurmFlag)
		}
	}

	// append mpi-flags to Container
	/*
		if mpiFlags, ok := h.Pod.GetAnnotations()["slurm-job/mpi-flags"]; ok {
			if mpiFlags != "true" {
				mpi := append([]string{Executables.MpiexecPath(), "-np", "$SLURM_NTASKS"}, strings.Split(mpiFlags, " ")...)
				singularityCommand = append(mpi, singularityCommand...)
			}
		}
	*/

	sbatchTemplate, err := template.New(h.Name).
		Funcs(sprig.FuncMap()).
		Option("missingkey=error").Parse(SbatchScriptTemplate)
	if err != nil {
		/*-- template errors should be expected from the custom fields where users can inject shitty input.	--*/
		SystemError(err, "sbatch template error")
	}

	/*---------------------------------------------------
	 * Generate Pod sbatchScript from Sbatch SubmitTemplate + Fields
	 *---------------------------------------------------*/
	sbatchScriptPath := h.podDirectory.SubmitJobPath()

	logger.Info(" * Generating Sbatch script", "path", sbatchScriptPath)

	sbatchScript := strings.Builder{}

	if err := sbatchTemplate.Execute(&sbatchScript, fields); err != nil {
		/*-- since both the template and fields are internal to the code, the evaluation should always succeed	--*/
		SystemError(err, "failed to evaluate sbatch template")
	}

	if err := os.WriteFile(sbatchScriptPath, []byte(sbatchScript.String()), compute.ContainerJobPermissions); err != nil {
		SystemError(err, "unable to write sbatch sbatchScript in file '%s'", fields.VirtualEnv.ConstructorPath)
	}

	/*---------------------------------------------------
	 * Submit job to Slurm, and store the JobID
	 *---------------------------------------------------*/
	logger.Info(" * Submitting sbatch to Slurm", "scriptPath", fields.VirtualEnv.ConstructorPath)

	jobID, err := SubmitJob(sbatchScriptPath)
	if err != nil {
		SystemError(err, "failed to submit job")
	}

	/*-- Update Pod with the JobID --*/
	SetPodID(h.Pod, JobIDTypeSlurm, jobID)
	if err != nil {
		SystemError(err, "failed to submit job")
	}

	h.logger.Info(" * Associating Slurm Jod ID to Pod ", "jobID", jobID)

	/*-- Needed as it will follow a GetPod()--*/
	SavePod(ctx, h.Pod)

	return nil
}

func (h *podHandler) buildContainer(container *corev1.Container) Container {
	h.logger.Info(" == Apptainer ==", "container", container.Name)

	containerPath := h.podDirectory.Container(container.Name)

	/*---------------------------------------------------
	 * Prepare Environment Variables
	 *---------------------------------------------------*/
	envfilePath := containerPath.EnvFilePath()

	h.logger.Info(" * Preparing env-file", "path", envfilePath)

	var envfile strings.Builder

	/*-- Set pod-wide variables --*/
	for _, envVar := range h.podEnvVariables {
		envfile.WriteString(fmt.Sprintf("%s=%s\n", envVar.Name, envVar.Value))
	}

	/*-- Set container-specific variables --*/
	for _, envVar := range container.Env {
		envfile.WriteString(fmt.Sprintf("%s=%s\n", envVar.Name, envVar.Value))
	}

	if err := os.WriteFile(envfilePath, []byte(envfile.String()), compute.PodGlobalDirectoryPermissions); err != nil {
		SystemError(err, "cannot write env file for container '%s' of pod '%s'", container, h.podKey)
	}

	/*---------------------------------------------------
	 * Prepare fields for Container Template
	 *---------------------------------------------------*/
	h.logger.Info(" * Setting Template Args")

	return Container{
		InstanceName: fmt.Sprintf("%s_%s_%s", h.Pod.GetNamespace(), h.Pod.GetName(), container.Name),
		Image:        compute.Environment.ContainerRegistry + container.Image,
		EnvFilePath:  envfilePath,
		Binds: func() []string {
			mountArgs := make([]string, 0, len(container.VolumeMounts))

			for _, mountVar := range container.VolumeMounts {
				mountArgs = append(mountArgs, h.podDirectory.Mountpaths(mountVar))
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
