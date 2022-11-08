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
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"text/template"

	"github.com/carv-ics-forth/hpk/compute"
	"github.com/carv-ics-forth/hpk/pkg/envvars"
	"github.com/carv-ics-forth/hpk/pkg/fieldpath"
	"github.com/carv-ics-forth/hpk/pkg/resourcemanager"
	"github.com/go-logr/logr"
	"github.com/hashicorp/go-multierror"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	k8errors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/json"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var escapeScripts = regexp.MustCompile(`(--[A-Za-z0-9\-]+=)(\$[{\(][A-Za-z0-9_]+[\)\}])`)

type podHandler struct {
	*corev1.Pod

	podKey client.ObjectKey

	podEnvVariables []corev1.EnvVar
	podDirectory    PodPath

	logger logr.Logger
}

func GetPod(podRef client.ObjectKey) (*corev1.Pod, error) {
	encodedPodFilepath := PodRuntimeDir(podRef).EncodedJSONPath()

	encodedPod, err := os.ReadFile(encodedPodFilepath)
	if err != nil {
		// if not found, it means that VirtualEnvironment that:
		// 1) the pod is  not scheduled on this node (user's problem).
		// 2) someone removed the file (administrator's problem).
		// 3) we have a race condition (our problem).
		return nil, errors.Wrapf(err, "cannot read pod description file '%s'", encodedPodFilepath)
	}

	var pod corev1.Pod

	if err := json.Unmarshal(encodedPod, &pod); err != nil {
		return nil, errors.Wrapf(err, "cannot decode pod description file '%s'", encodedPodFilepath)
	}

	return &pod, nil
}

func GetPods() ([]*corev1.Pod, error) {
	var pods []*corev1.Pod
	var merr *multierror.Error

	/*---------------------------------------------------
	 * Iterate the filesystem and extract local pods
	 *---------------------------------------------------*/
	filepath.Walk(RuntimeDir, func(path string, info os.FileInfo, err error) error {
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

func SavePod(pod *corev1.Pod) error {
	podKey := client.ObjectKeyFromObject(pod)
	logger := DefaultLogger.WithValues("pod", podKey)

	encodedPod, err := json.Marshal(pod)
	if err != nil {
		return errors.Wrapf(err, "cannot marshall pod '%s' to json", podKey)
	}

	encodedPodFile := PodRuntimeDir(podKey).EncodedJSONPath()

	if err := os.WriteFile(encodedPodFile, encodedPod, PodSpecJsonFilePermissions); err != nil {
		return errors.Wrapf(err, "cannot write pod json file '%s'", encodedPodFile)
	}

	logger.Info("DISK <-- Event: VirtualEnvironment Status Renewed",
		"version", pod.ResourceVersion,
		"phase", pod.Status.Phase,
	)

	return nil
}

func DeletePod(pod *corev1.Pod) error {
	podKey := client.ObjectKeyFromObject(pod)
	// logger := DefaultLogger.WithValues("pod", podKey)

	/*---------------------------------------------------
	 * Cancel Slurm Jobs
	 *---------------------------------------------------*/
	/*
			DeletePod takes a Kubernetes VirtualEnvironment and deletes it from the provider.
			TODO: Once a pod is deleted, the provider is expected
			to call the NotifyPods callback with a terminal pod status
			where all the containers are in a terminal state, as well as the pod.
			DeletePod may be called multiple times for the same pod

		{
			// Fail this condition because containers are no longer individual Slurm jobs,
			// but they are processes within a pod/job.
			// TODO: find a way to use the slurm identifier to cancel the job

			var merr *multierror.Error

			for i, container := range pod.Status.InitContainerStatuses {
				/*-- Cancel Process Job --* /
				if container.ContainerID != "" {
					/*-- Extract container_id from raw format '<type>://<container_id>'. --* /
					containerID := strings.Split(container.ContainerID, containerIDType)[1]

					_, err := CancelJob(containerID)
					if err != nil {
						merr = multierror.Append(merr, errors.Wrapf(err, "failed to cancel job '%s'", container.ContainerID))
					}
				}

				/*-- Mark the Process as Terminated --* /
				pod.Status.InitContainerStatuses[i].State.Terminated = &corev1.ContainerStateTerminated{
					Reason:     "PodIsDeleted",
					Message:    "VirtualEnvironment is being deleted",
					FinishedAt: metav1.Now(),
				}
			}

			for i, container := range pod.Status.ContainerStatuses {
				/*-- Cancel Process Job --* /
				if container.ContainerID != "" {
					/*-- Extract container_id from raw format '<type>://<container_id>'. --* /
					containerID := strings.Split(container.ContainerID, containerIDType)[1]

					_, err := CancelJob(containerID)
					if err != nil {
						merr = multierror.Append(merr, errors.Wrapf(err, "failed to cancel job '%s'", container.ContainerID))
					}
				}

				/*-- Mark the Process as Terminated --* /
				pod.Status.ContainerStatuses[i].State.Terminated = &corev1.ContainerStateTerminated{
					Reason:     "PodIsDeleted",
					Message:    "VirtualEnvironment is being deleted",
					FinishedAt: metav1.Now(),
				}
			}

			if merr.ErrorOrNil() != nil {
				logger.Error(merr, "VirtualEnvironment termination error")
			}
		}
	*/

	/*---------------------------------------------------
	 * Remove VirtualEnvironment directory
	 *---------------------------------------------------*/
	podDir := PodRuntimeDir(podKey)

	if err := os.RemoveAll(string(podDir)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.Wrapf(err, "failed to delete pod directory %s'", podDir)
	}

	return nil
}

func CreatePod(ctx context.Context, pod *corev1.Pod, rmanager *resourcemanager.ResourceManager) error {
	podKey := client.ObjectKeyFromObject(pod)
	logger := DefaultLogger.WithValues("pod", podKey)

	/*---------------------------------------------------
	 * Pre-Populate Process.Status from Process.Spec
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
	 * Prepare VirtualEnvironment for execution on Slurm
	 *---------------------------------------------------*/
	logger.Info("* Creating Virtual Environment ==")

	/*-- Create the directory for virtual environment --*/
	virtualEnvironmentDir := PodRuntimeDir(podKey).VirtualEnvironmentDir()

	if err := os.MkdirAll(string(virtualEnvironmentDir), PodGlobalDirectoryPermissions); err != nil {
		return errors.Wrapf(err, "Cant create sbatch directory '%s'", virtualEnvironmentDir)
	}

	/*-- Setting a list of services the pod in a namespace should see --*/
	podEnvVariables, err := getServiceEnvVarMap(ctx, pod.GetNamespace())
	if err != nil {
		return errors.Wrapf(err, "failed to prepare environment variables")
	}

	/*-- Create the pod handler --*/
	h := podHandler{
		Pod:             pod,
		podKey:          podKey,
		podDirectory:    PodRuntimeDir(podKey),
		podEnvVariables: podEnvVariables,
		logger:          logger,
	}

	/*-- Kind of Journalling --*/
	if err := SavePod(pod); err != nil {
		return errors.Wrapf(err, "failed to save pod description")
	}

	/*---------------------------------------------------
	 * Prepare Volumes on the VirtualEnvironment
	 *---------------------------------------------------*/
	logger.Info(" * Creating Backend Volumes")

	if err := h.makeMounts(rmanager); err != nil {
		return errors.Wrapf(err, "failed to prepare runtime dir for pod '%s'", h.podKey)
	}

	/*---------------------------------------------------
	 * Set listeners for async changes on the VirtualEnvironment
	 *---------------------------------------------------*/
	logger.Info(" * Setting Filesystem Notifiers", "watchPath", h.podDirectory)

	// because fswatch does not work recursively, we cannot have the container directories nested within the pod.
	// instead, we use a flat directory in the format "podir/containername.{jid,stdout,stdour,...}"
	if err := fswatcher.Add(string(h.podDirectory)); err != nil {
		return errors.Wrapf(err, "register to fsnotify has failed for path '%s'", h.podDirectory)
	}

	/*---------------------------------------------------
	 * Build Process Commands for Init Containers
	 *---------------------------------------------------*/
	logger.Info(" * Setting apptainer for init containers")

	var initContainers []Process

	for i, container := range pod.Spec.InitContainers {
		job, err := h.buildApptainerCommands(&pod.Spec.InitContainers[i])
		if err != nil {
			return errors.Wrapf(err, "creation request failed for container '%s'", container.Name)
		}

		initContainers = append(initContainers, job)
	}

	/*---------------------------------------------------
	 * Build Process Commands for Containers
	 *---------------------------------------------------*/
	logger.Info(" * Setting apptainer for containers")

	var containers []Process

	for i, container := range pod.Spec.Containers {
		job, err := h.buildApptainerCommands(&pod.Spec.Containers[i])
		if err != nil {
			return errors.Wrapf(err, "creation request failed for container '%s'", container.Name)
		}

		containers = append(containers, job)
	}

	/*---------------------------------------------------
	 * Prepare Fields for Sbatch Templates
	 *---------------------------------------------------*/
	logger.Info(" * Preparing Fields for Sbatch")

	/*-- Set HPK-defined fields for Sbatch Template --*/
	job := SBatchTemplateFields{
		ComputeEnv: compute.Environment,
		Pod:        h.podKey,
		VirtualEnv: VirtualEnvironment{
			ConstructorPath: h.podDirectory.ConstructorPath(),
			IDPath:          h.podDirectory.IDPath(),
			IPAddressPath:   h.podDirectory.IPAddressPath(),
			StdoutPath:      h.podDirectory.StdoutPath(),
			StderrPath:      h.podDirectory.StderrPath(),
			ExitCodePath:    h.podDirectory.ExitCodePath(),
		},
		InitContainers: initContainers,
		Containers:     containers,
		Options:        SbatchOptions{},
	}

	/*-- Set user-defined sbatch flags (e.g, From Argo) --*/
	if slurmFlags, ok := h.Pod.GetAnnotations()["slurm-job/flags"]; ok {
		for _, slurmFlag := range strings.Split(slurmFlags, " ") {
			job.Options.CustomFlags = append(job.Options.CustomFlags,
				"\n#SBATCH "+slurmFlag)
		}
	}

	// append mpi-flags to Process
	/*
		if mpiFlags, ok := h.VirtualEnvironment.GetAnnotations()["slurm-job/mpi-flags"]; ok {
			if mpiFlags != "true" {
				mpi := append([]string{Executables.MpiexecPath(), "-np", "$SLURM_NTASKS"}, strings.Split(mpiFlags, " ")...)
				singularityCommand = append(mpi, singularityCommand...)
			}
		}
	*/

	sbatchTemplate, err := template.New(h.Name).Option("missingkey=error").Parse(SBatchTemplate)
	if err != nil {
		/*-- template errors should be expected from the custom fields where users can inject shitty input.	--*/
		return errors.Wrapf(err, "sbatch template error")
	}

	/*---------------------------------------------------
	 * Generate VirtualEnvironment sbatchScript from Sbatch SubmitTemplate + Fields
	 *---------------------------------------------------*/
	logger.Info(" * Generating Sbatch script")

	sbatchScript := strings.Builder{}
	sbatchScriptPath := h.podDirectory.SubmitJobPath()

	if err := sbatchTemplate.Execute(&sbatchScript, job); err != nil {
		/*-- since both the template and fields are internal to the code, the evaluation should always succeed	--*/
		panic(errors.Wrapf(err, "failed to evaluate sbatch template"))
	}

	if err := os.WriteFile(sbatchScriptPath, []byte(sbatchScript.String()), ContainerJobPermissions); err != nil {
		return errors.Wrapf(err, "unable to write sbatch sbatchScript in file '%s'", job.VirtualEnv.ConstructorPath)
	}

	/*---------------------------------------------------
	 * Submit Sbatch to Slurm and get Submission Results
	 *---------------------------------------------------*/
	logger.Info(" * Submit sbatch to Slurm", "scriptPath", job.VirtualEnv.ConstructorPath)

	if err := h.submitJob(sbatchScriptPath); err != nil {
		return errors.Wrapf(err, "sbatch submission error")
	}

	return nil
}

func (h *podHandler) buildApptainerCommands(container *corev1.Container) (Process, error) {
	h.logger.Info("== Build Process Command  ==", "container", container.Name)

	/*---------------------------------------------------
	 * Prepare fields for Process Template
	 *---------------------------------------------------*/
	h.logger.Info(" * Set flags and args")

	tFields := ApptainerTemplateFields{
		Apptainer: func() string {
			if container.Command == nil {
				return ApptainerRun
			} else {
				return ApptainerExec
			}
		}(),
		Image:   compute.Environment.ContainerRegistry + container.Image,
		Command: container.Command,
		Args:    container.Args,
		Environment: func() []string {
			var envArgs []string

			/*-- Set pod-wide variables --*/
			for _, envVar := range h.podEnvVariables {
				envArgs = append(envArgs, envVar.Name+"="+envVar.Value)
			}

			/*-- Set container-specific variables --*/
			for _, envVar := range container.Env {
				envArgs = append(envArgs, envVar.Name+"="+envVar.Value)
			}

			return envArgs
		}(),
		Bind: func() []string {
			mountArgs := make([]string, 0, len(container.VolumeMounts))

			for _, mountVar := range container.VolumeMounts {
				mountArgs = append(mountArgs, h.podDirectory.Mountpaths(mountVar))
			}

			return mountArgs
		}(),
	}

	/*---------------------------------------------------
	 * Build the Process Command
	 *---------------------------------------------------*/
	h.logger.Info(" * Finalize Process Command")

	submitTpl, err := template.New(h.Name).Option("missingkey=error").Parse(ApptainerTemplate)
	if err != nil {
		/*-- template errors should be expected from the custom fields where users can inject shitty input.	--*/
		return Process{}, errors.Wrapf(err, "Process template error")
	}

	var apptainerCmd strings.Builder

	if err := submitTpl.Execute(&apptainerCmd, tFields); err != nil {
		/*-- since both the template and fields are internal to the code, the evaluation should always succeed	--*/
		panic(errors.Wrapf(err, "failed to evaluate Process template"))
	}

	containerPath := h.podDirectory.Container(container.Name)

	return Process{
		Command:      apptainerCmd.String(),
		IDPath:       containerPath.IDPath(),
		StdoutPath:   containerPath.StdoutPath(),
		StderrPath:   containerPath.StderrPath(),
		ExitCodePath: containerPath.ExitCodePath(),
	}, nil
}

func (h *podHandler) submitJob(scriptFilePath string) error {
	out, err := SubmitJob(scriptFilePath)
	if err != nil {
		h.logger.Error(err, "sbatch submission error", "out", out)

		panic(errors.Wrapf(err, "sbatch submission error"))
	}

	expectedOutput := regexp.MustCompile(`Submitted batch job (?P<jid>\d+)`)
	jid := expectedOutput.FindStringSubmatch(out)

	intJobID, err := strconv.Atoi(jid[1])
	if err != nil {
		return errors.Wrap(err, "Cant convert jid as integer. Parsed irregular output!")
	}

	h.logger.Info(" * Job has been submitted to Slurm", "jobID", intJobID)

	return nil
}

func (h *podHandler) makeMounts(rmanager *resourcemanager.ResourceManager) error {
	/*---------------------------------------------------
	 * Copy volumes from local VirtualEnvironment to remote HPCBackend
	 *---------------------------------------------------*/
	for _, vol := range h.Pod.Spec.Volumes {
		switch {
		case vol.VolumeSource.ConfigMap != nil:
			/*---------------------------------------------------
			 * ConfigMap
			 * Example:
				volumes:
				 - name: config-volume
				   configMap:
					 name: game-config
			 *---------------------------------------------------*/
			source := vol.VolumeSource.ConfigMap

			configMap, err := rmanager.GetConfigMap(source.Name, h.Pod.GetNamespace())
			if k8errors.IsNotFound(err) {
				if source.Optional != nil && !*source.Optional {
					return errors.Wrapf(err, "Configmap '%s' is required by VirtualEnvironment '%s' and does not exist", source.Name, h.Pod.GetName())
				}
			}

			if err != nil {
				return errors.Wrapf(err, "Error getting configmap '%s' from API server", h.Pod.GetName())
			}

			// .hpk/namespace/podName/volName/*
			podConfigMapDir, err := h.podDirectory.CreateSubDirectory(vol.Name)
			if err != nil {
				return errors.Wrapf(err, "cannot create dir '%s' for configMap", podConfigMapDir)
			}

			for k, v := range configMap.Data {
				// TODO: Ensure that these files are deleted in failure cases
				fullPath := filepath.Join(podConfigMapDir, k)

				if err := os.WriteFile(fullPath, []byte(v), fs.FileMode(*source.DefaultMode)); err != nil {
					return errors.Wrapf(err, "cannot write config map file '%s'", fullPath)
				}
			}

		case vol.VolumeSource.Secret != nil:
			/*---------------------------------------------------
			 * Secret
			 *---------------------------------------------------*/
			source := vol.VolumeSource.Secret

			secret, err := rmanager.GetSecret(source.SecretName, h.Pod.GetNamespace())
			if k8errors.IsNotFound(err) {
				if source.Optional != nil && !*source.Optional {
					return errors.Wrapf(err, "Secret '%s' not found in namespace '%s'", source.SecretName, h.Pod.GetNamespace())
				}
			}
			if err != nil {
				return errors.Wrapf(err, "Error getting secret '%s' from API server", source.SecretName)
			}

			// .hpk/namespace/podName/secretName/*
			podSecretDir, err := h.podDirectory.CreateSubDirectory(vol.Name)
			if err != nil {
				return errors.Wrapf(err, "cannot create dir '%s' for secrets", podSecretDir)
			}

			for k, v := range secret.Data {
				fullPath := filepath.Join(podSecretDir, k)

				if err := os.WriteFile(fullPath, v, fs.FileMode(*source.DefaultMode)); err != nil {
					return errors.Wrapf(err, "Could not write secret file %s", fullPath)
				}
			}

		case vol.VolumeSource.EmptyDir != nil:
			/*---------------------------------------------------
			 * EmptyDir
			 *---------------------------------------------------*/
			emptyDir, err := h.podDirectory.CreateSubDirectory(vol.Name)
			if err != nil {
				return errors.Wrapf(err, "cannot create dir '%s' for emptyDir", emptyDir)
			}
			// without size limit for now

		case vol.VolumeSource.DownwardAPI != nil:
			/*---------------------------------------------------
			 * Downward API
			 *---------------------------------------------------*/
			downApiDir, err := h.podDirectory.CreateSubDirectory(vol.Name)
			if err != nil {
				return errors.Wrapf(err, "cannot create dir '%s' for downwardApi", downApiDir)
			}

			for _, item := range vol.DownwardAPI.Items {
				itemPath := filepath.Join(downApiDir, item.Path)
				value, err := fieldpath.ExtractFieldPathAsString(h.Pod, item.FieldRef.FieldPath)
				if err != nil {
					return err
				}

				if err := os.WriteFile(itemPath, []byte(value), fs.FileMode(*vol.Projected.DefaultMode)); err != nil {
					return errors.Wrapf(err, "cannot write config map file '%s'", itemPath)
				}
			}

		case vol.VolumeSource.HostPath != nil:
			/*---------------------------------------------------
			 * HostPath
			 *---------------------------------------------------*/
			switch *vol.VolumeSource.HostPath.Type {
			case corev1.HostPathUnset:
				// For backwards compatible, leave it empty if unset
				if path, err := h.podDirectory.CreateSubDirectory(vol.Name); err != nil {
					return errors.Wrapf(err, "cannot create HostPathUnset at path '%s'", path)
				}

			case corev1.HostPathDirectoryOrCreate:
				// If nothing exists at the given path, an empty directory will be created there
				// as needed with file mode 0755, having the same group and ownership with Kubelet.
				if path, err := h.podDirectory.CreateSubDirectory(vol.Name); err != nil {
					return errors.Wrapf(err, "cannot create HostPathDirectoryOrCreate at path '%s'", path)
				}
			case corev1.HostPathDirectory:
				// A directory must exist at the given path
				if path, err := h.podDirectory.CreateSubDirectory(vol.Name); err != nil {
					return errors.Wrapf(err, "cannot create HostPathDirectory at path '%s'", path)
				}

			case corev1.HostPathFileOrCreate:
				// If nothing exists at the given path, an empty file will be created there
				// as needed with file mode 0644, having the same group and ownership with Kubelet.
				// .hpk/podName/volName/*
				f, err := os.Create(string(h.podDirectory))
				if err != nil {
					return errors.Wrapf(err, "cannot create '%s'", string(h.podDirectory))
				}

				if err := f.Close(); err != nil {
					return errors.Wrapf(err, "cannot close file '%s'", string(h.podDirectory))
				}
			case corev1.HostPathFile:
				// A file must exist at the given path
				// .hpk/podName/volName/*
				f, err := os.Create(string(h.podDirectory))
				if err != nil {
					return errors.Wrapf(err, "cannot create '%s'", string(h.podDirectory))
				}

				if err := f.Close(); err != nil {
					return errors.Wrapf(err, "cannot close file '%s'", string(h.podDirectory))
				}
			case corev1.HostPathSocket, corev1.HostPathCharDev, corev1.HostPathBlockDev:
				// A UNIX socket/char device/ block device must exist at the given path
				continue
			default:
				panic("bug")
			}

		case vol.VolumeSource.Projected != nil:
			/*---------------------------------------------------
			 * Projected
			 *---------------------------------------------------*/
			projectedVolPath, err := h.podDirectory.CreateSubDirectory(vol.Name)
			if err != nil {
				return errors.Wrapf(err, "cannot create dir '%s' for projected volume", projectedVolPath)
			}

			for _, projectedSrc := range vol.Projected.Sources {
				switch {
				case projectedSrc.DownwardAPI != nil:
					/*---------------------------------------------------
					 * Projected DownwardAPI
					 *---------------------------------------------------*/
					for _, item := range projectedSrc.DownwardAPI.Items {
						itemPath := filepath.Join(projectedVolPath, item.Path)

						value, err := fieldpath.ExtractFieldPathAsString(h.Pod, item.FieldRef.FieldPath)
						if err != nil {
							return err
						}

						if err := os.WriteFile(itemPath, []byte(value), fs.FileMode(*vol.Projected.DefaultMode)); err != nil {
							return errors.Wrapf(err, "cannot write config map file '%s'", itemPath)
						}
					}

				case projectedSrc.ServiceAccountToken != nil:
					/*---------------------------------------------------
					 * Projected ServiceAccountToken
					 *---------------------------------------------------*/
					serviceAccount, err := rmanager.GetServiceAccount(h.Pod.Spec.ServiceAccountName, h.Pod.GetNamespace())
					if err != nil {
						return errors.Wrapf(err, "Error getting service account '%s' from API server", h.Pod.Spec.ServiceAccountName)
					}

					if serviceAccount == nil {
						panic("this should never happen ")
					}

					if serviceAccount.AutomountServiceAccountToken != nil && *serviceAccount.AutomountServiceAccountToken {
						for _, secretRef := range serviceAccount.Secrets {
							secret, err := rmanager.GetSecret(secretRef.Name, h.Pod.GetNamespace())
							if err != nil {
								return errors.Wrapf(err, "get secret error")
							}

							// TODO: Update upon exceeded expiration date
							// TODO: Ensure that these files are deleted in failure cases
							fullPath := filepath.Join(projectedVolPath, projectedSrc.ServiceAccountToken.Path)
							if err := os.WriteFile(fullPath, secret.Data[projectedSrc.ServiceAccountToken.Path], fs.FileMode(0o766)); err != nil {
								return errors.Wrapf(err, "cannot write config map file '%s'", fullPath)
							}
						}
					}

				case projectedSrc.ConfigMap != nil:
					/*---------------------------------------------------
					 * Projected ConfigMap
					 *---------------------------------------------------*/
					configMap, err := rmanager.GetConfigMap(projectedSrc.ConfigMap.Name, h.Pod.GetNamespace())
					{ // err check
						if projectedSrc.ConfigMap.Optional != nil && !*projectedSrc.ConfigMap.Optional {
							return errors.Wrapf(err, "Configmap '%s' is required by VirtualEnvironment '%s' and does not exist", projectedSrc.ConfigMap.Name, h.Pod.GetName())
						}

						if err != nil {
							return errors.Wrapf(err, "Error getting configmap '%s' from API server", h.Pod.Name)
						}

						if configMap == nil {
							continue
						}
					}

					for k, item := range configMap.Data {
						// TODO: Ensure that these files are deleted in failure cases
						itemPath := filepath.Join(projectedVolPath, k)
						if err := os.WriteFile(itemPath, []byte(item), PodGlobalDirectoryPermissions); err != nil {
							return errors.Wrapf(err, "cannot write config map file '%s'", itemPath)
						}
					}

				case projectedSrc.Secret != nil:
					/*---------------------------------------------------
					 * Projected Secret
					 *---------------------------------------------------*/
					secret, err := rmanager.GetSecret(projectedSrc.Secret.Name, h.Pod.GetNamespace())
					{ // err check
						if projectedSrc.Secret.Optional != nil && !*projectedSrc.Secret.Optional {
							return errors.Wrapf(err, "Secret '%s' is required by VirtualEnvironment '%s' and does not exist", projectedSrc.Secret.Name, h.Pod.GetName())
						}

						if err != nil {
							return errors.Wrapf(err, "Error getting secret '%s' from API server", h.Pod.Name)
						}

						if secret == nil {
							continue
						}
					}

					for k, item := range secret.Data {
						// TODO: Ensure that these files are deleted in failure cases
						itemPath := filepath.Join(projectedVolPath, k)
						if err := os.WriteFile(itemPath, item, PodGlobalDirectoryPermissions); err != nil {
							return errors.Wrapf(err, "cannot write config map file '%s'", itemPath)
						}
					}
				}
			}
		}
	}

	return nil
}

// getServiceEnvVarMap makes a map[string]string of env vars for services a  pod in namespace ns should see.
// However, the kubelet implementation works on for Services with ClusterIPs.
// In our case, the Services point directly to Pod IP's, and the implementation does not work.
// The solution is to retrieve services from Endpoints.
//
// Original:
// https://github.com/kubernetes/kubernetes/blob/1139bb177b2b35611c5ca16cc82f0e41a8bb107e/pkg/kubelet/kubelet_pods.go#L575
func getServiceEnvVarMap(ctx context.Context, namespace string) ([]corev1.EnvVar, error) {
	/*---------------------------------------------------
	 * Get all service resources from master
	 *---------------------------------------------------*/
	var endpointsList corev1.EndpointsList

	if err := compute.K8SClient.List(ctx, &endpointsList, &client.ListOptions{
		LabelSelector: labels.Everything(),
	}); err != nil {
		return nil, errors.Wrap(err, "failed to list services when setting up env vars")
	}

	/*---------------------------------------------------
	 * Populate services into service environment variables.
	 *---------------------------------------------------*/
	var mappedEndpoints []*corev1.Endpoints

	for i, endpoint := range endpointsList.Items {
		// ignore endpoints without IPs
		if len(endpoint.Subsets) == 0 {
			continue
		}

		// We always want to add environment variabled for master services
		// from the master service namespace, even if enableServiceLinks is false.
		// We also add environment variables for other services in the same
		// namespace, if enableServiceLinks is true.
		if endpoint.GetNamespace() == namespace ||
			endpoint.GetNamespace() == metav1.NamespaceDefault {
			mappedEndpoints = append(mappedEndpoints, &endpointsList.Items[i])
		}
	}

	return envvars.FromEndpoints(mappedEndpoints), nil
}
