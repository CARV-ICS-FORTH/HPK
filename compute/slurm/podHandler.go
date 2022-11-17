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
	"github.com/go-logr/logr"
	"github.com/hashicorp/go-multierror"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	k8errors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/json"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type podHandler struct {
	*corev1.Pod

	podKey client.ObjectKey

	podEnvVariables []corev1.EnvVar
	podDirectory    PodPath

	logger logr.Logger
}

func LoadPod(podRef client.ObjectKey) *corev1.Pod {
	encodedPodFilepath := PodRuntimeDir(podRef).EncodedJSONPath()

	encodedPod, err := os.ReadFile(encodedPodFilepath)
	if os.IsNotExist(err) {
		/*
				if err not found, it means that:
			 	1) the pod is  not scheduled on this node (user's problem).
				2) someone removed the file (administrator's problem).
				3) we have a race condition (our problem).
		*/
		return nil
	} else if err != nil {
		SystemError(err, "failed to read pod description file '%s'", encodedPodFilepath)
	}

	var pod corev1.Pod

	if err := json.Unmarshal(encodedPod, &pod); err != nil {
		SystemError(err, "cannot decode pod description file '%s'", encodedPodFilepath)
	}

	return &pod
}

func SavePod(pod *corev1.Pod) {
	podKey := client.ObjectKeyFromObject(pod)
	logger := DefaultLogger.WithValues("pod", podKey)

	encodedPod, err := json.Marshal(pod)
	if err != nil {
		SystemError(err, "cannot marshall pod '%s' to json", podKey)
	}

	encodedPodFile := PodRuntimeDir(podKey).EncodedJSONPath()

	if err := os.WriteFile(encodedPodFile, encodedPod, PodSpecJsonFilePermissions); err != nil {
		SystemError(err, "cannot write pod json file '%s'", encodedPodFile)
	}

	logger.Info("DISK <-- Event: VirtualEnvironment Status Renewed",
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

/*
DeletePod takes a Pod Reference and deletes the Pod from the provider.
DeletePod may be called multiple times for the same pod.

Notice that by using the reference, we operate on the local copy instead of the remote. This serves two purposes:
1) We can extract updated information from .spec (Kubernetes only fetches .Status)
2) We can have "fresh" information that is not yet propagated to Kubernetes
*/
func DeletePod(podKey client.ObjectKey) error {
	pod := LoadPod(podKey)
	if pod == nil {
		return nil
	}

	logger := DefaultLogger.WithValues("pod", podKey)

	/*---------------------------------------------------
	 * Cancel Slurm Job
	 *---------------------------------------------------*/
	logger.Info(" * Cancelling Slurm Job")

	idType, podID := ParsePodID(pod)

	if idType != IDTypeEmpty {
		out, err := CancelJob(podID)
		if err != nil {
			SystemError(err, "failed to cancel job '%s'. out: '%s'", podID, out)
		}

		logger.Info("Slurm Job has been cancelled", "job", podID, "out", out)
	} else {
		logger.Info(" * No Slurm ID was found.")
	}

	/*---------------------------------------------------
	 * Remove Pod Directory
	 *---------------------------------------------------*/
	logger.Info(" * Removing Pod Directory")

	podDir := PodRuntimeDir(podKey)

	if err := os.RemoveAll(string(podDir)); err != nil && !errors.Is(err, os.ErrNotExist) {
		SystemError(err, "failed to delete pod directory %s'", podDir)
	}

	/*
		TODO: Once a pod is deleted, the provider is expected
		to call the NotifyPods callback with a terminal pod status
		where all the containers are in a terminal state, as well as the pod.
	*/

	return nil
}

func CreatePod(ctx context.Context, pod *corev1.Pod) error {
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
	logger.Info("== Creating Virtual Environment ==")

	/*-- Create the directory for virtual environment --*/
	virtualEnvironmentDir := PodRuntimeDir(podKey).VirtualEnvironmentDir()

	if err := os.MkdirAll(string(virtualEnvironmentDir), PodGlobalDirectoryPermissions); err != nil {
		SystemError(err, "Cant create sbatch directory '%s'", virtualEnvironmentDir)
	}

	/*-- Setting a list of services the pod in a namespace should see --*/
	podEnvVariables, err := getServiceEnvVarMap(ctx, pod.GetNamespace())
	if err != nil {
		SystemError(err, "failed to prepare environment variables")
	}

	/*-- Create the pod handler --*/
	h := podHandler{
		Pod:             pod,
		podKey:          podKey,
		podDirectory:    PodRuntimeDir(podKey),
		podEnvVariables: podEnvVariables,
		logger:          logger,
	}

	/*-- Kind of Journaling --*/
	SavePod(pod)

	/*---------------------------------------------------
	 * Prepare Volumes on the VirtualEnvironment
	 *---------------------------------------------------*/
	logger.Info(" * Creating Backend Volumes")

	h.makeMounts(ctx)

	/*---------------------------------------------------
	 * Set listeners for async changes on the VirtualEnvironment
	 *---------------------------------------------------*/
	logger.Info(" * Setting Filesystem Notifiers", "watchPath", h.podDirectory)

	// because fswatch does not work recursively, we cannot have the container directories nested within the pod.
	// instead, we use a flat directory in the format "podir/containername.{jid,stdout,stdour,...}"
	if err := fswatcher.Add(string(h.podDirectory)); err != nil {
		SystemError(err, "register to fsnotify has failed for path '%s'", h.podDirectory)
	}

	/*---------------------------------------------------
	 * Build Process Commands for Init Containers
	 *---------------------------------------------------*/
	logger.Info(" * Setting apptainer for init containers")

	var initContainers []Process

	for i, container := range pod.Spec.InitContainers {
		job, err := h.makeApptainerCommands(&pod.Spec.InitContainers[i], true)
		if err != nil {
			SystemError(err, "creation request failed for container '%s'", container.Name)
		}

		initContainers = append(initContainers, job)
	}

	/*---------------------------------------------------
	 * Build Process Commands for Containers
	 *---------------------------------------------------*/
	logger.Info(" * Setting apptainer for containers")

	var containers []Process

	for i, container := range pod.Spec.Containers {
		job, err := h.makeApptainerCommands(&pod.Spec.Containers[i], false)
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
		SystemError(err, "sbatch template error")
	}

	/*---------------------------------------------------
	 * Generate VirtualEnvironment sbatchScript from Sbatch SubmitTemplate + Fields
	 *---------------------------------------------------*/
	logger.Info(" * Generating Sbatch script")

	sbatchScript := strings.Builder{}
	sbatchScriptPath := h.podDirectory.SubmitJobPath()

	if err := sbatchTemplate.Execute(&sbatchScript, job); err != nil {
		/*-- since both the template and fields are internal to the code, the evaluation should always succeed	--*/
		SystemError(err, "failed to evaluate sbatch template")
	}

	if err := os.WriteFile(sbatchScriptPath, []byte(sbatchScript.String()), ContainerJobPermissions); err != nil {
		SystemError(err, "unable to write sbatch sbatchScript in file '%s'", job.VirtualEnv.ConstructorPath)
	}

	/*---------------------------------------------------
	 * Submit job to Slurm, and store the JobID
	 *---------------------------------------------------*/
	logger.Info(" * Submit sbatch to Slurm", "scriptPath", job.VirtualEnv.ConstructorPath)

	jobID, err := h.submitJob(sbatchScriptPath)
	if err != nil {
		SystemError(err, "failed to submit job")
	}

	/*-- Update Pod with the JobID --*/
	SetPodID(h.Pod, IDTypeSlurm, jobID)
	if err != nil {
		SystemError(err, "failed to submit job")
	}

	SavePod(h.Pod)

	h.logger.Info(" * Job has been submitted to Slurm", "jobID", jobID)
	return nil
}

func (h *podHandler) makeApptainerCommands(container *corev1.Container, isInitContainer bool) (Process, error) {
	h.logger.Info(" == Apptainer ==", "container", container.Name)

	containerPath := h.podDirectory.Container(container.Name)

	/*---------------------------------------------------
	 * Prepare Environment Variables
	 *---------------------------------------------------*/
	var envfile strings.Builder
	envfilePath := containerPath.EnvFilePath()

	/*-- Set pod-wide variables --*/
	for _, envVar := range h.podEnvVariables {
		envfile.WriteString(fmt.Sprintf("%s='%s'\n", envVar.Name, envVar.Value))
	}

	/*-- Set container-specific variables --*/
	for _, envVar := range container.Env {
		envfile.WriteString(fmt.Sprintf("%s='%s'\n", envVar.Name, envVar.Value))
	}

	if err := os.WriteFile(envfilePath, []byte(envfile.String()), PodGlobalDirectoryPermissions); err != nil {
		SystemError(err, "cannot write env file for container '%s' of pod '%s'", container, h.podKey)
	}

	/*---------------------------------------------------
	 * Prepare fields for Process Template
	 *---------------------------------------------------*/
	h.logger.Info(" * Set flags and execution args")

	tFields := ApptainerTemplateFields{
		Image:               compute.Environment.ContainerRegistry + container.Image,
		Command:             container.Command,
		Args:                container.Args,
		EnvironmentFilePath: envfilePath,
		Bind: func() []string {
			mountArgs := make([]string, 0, len(container.VolumeMounts))

			for _, mountVar := range container.VolumeMounts {
				mountArgs = append(mountArgs, h.podDirectory.Mountpaths(mountVar))
			}

			return mountArgs
		}(),
	}

	//	if isInitContainer {
	if container.Command == nil {
		tFields.Apptainer = ApptainerRun
	} else {
		tFields.Apptainer = ApptainerExec
	}
	/*
		} else {
			tFields.Apptainer = ApptainerStart

			/*-- $(hostname)-${USER} will be evaluated at execution time --* /
			instanceName := "$(hostname)-${USER}-" + container.Name
			tFields.InstanceName = &instanceName
		}
	*/

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

	return Process{
		Command:      apptainerCmd.String(),
		IDPath:       containerPath.IDPath(),
		StdoutPath:   containerPath.StdoutPath(),
		StderrPath:   containerPath.StderrPath(),
		ExitCodePath: containerPath.ExitCodePath(),
	}, nil
}

func (h *podHandler) submitJob(scriptFilePath string) (string, error) {
	out, err := SubmitJob(scriptFilePath)
	if err != nil {
		SystemError(err, "sbatch submission error. out : '%s'", out)
	}

	expectedOutput := regexp.MustCompile(`Submitted batch job (?P<jid>\d+)`)
	jid := expectedOutput.FindStringSubmatch(out)

	if _, err := strconv.Atoi(jid[1]); err != nil {
		SystemError(err, "Cant convert jid as integer. Parsed irregular output!")
	}
	return jid[1], nil
}

func (h *podHandler) makeMounts(ctx context.Context) {
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
			var configMap corev1.ConfigMap

			source := vol.VolumeSource.ConfigMap

			key := types.NamespacedName{Namespace: h.Pod.GetNamespace(), Name: source.Name}

			if err := compute.K8SClient.Get(ctx, key, &configMap); k8errors.IsNotFound(err) {
				if source.Optional != nil && *source.Optional == false {
					PodError(h.Pod, ReasonObjectNotFound, "configMap '%s'", key)
				}

				/*-- configMap is optional, and we can safely skip the step --*/
				return
			} else if err != nil {
				SystemError(err, "error getting configMap '%s'", key)
			}

			// .hpk/namespace/podName/volName/*
			podConfigMapDir, err := h.podDirectory.CreateSubDirectory(vol.Name)
			if err != nil {
				SystemError(err, "cannot create dir '%s' for configMap", podConfigMapDir)
			}

			for k, v := range configMap.Data {
				// TODO: Ensure that these files are deleted in failure cases
				fullPath := filepath.Join(podConfigMapDir, k)

				if err := os.WriteFile(fullPath, []byte(v), fs.FileMode(*source.DefaultMode)); err != nil {
					SystemError(err, "cannot write config map file '%s'", fullPath)
				}
			}

		case vol.VolumeSource.Secret != nil:
			/*---------------------------------------------------
			 * Secret
			 *---------------------------------------------------*/
			var secret corev1.Secret

			source := vol.VolumeSource.Secret

			key := types.NamespacedName{Namespace: h.Pod.GetNamespace(), Name: source.SecretName}

			if err := compute.K8SClient.Get(ctx, key, &secret); k8errors.IsNotFound(err) {
				if source.Optional != nil && *source.Optional == false {
					PodError(h.Pod, ReasonObjectNotFound, "secret '%s'", key)
				}

				/*-- secret is optional, and we can safely skip the step --*/
				return
			} else if err != nil {
				SystemError(err, "error getting secret '%s'", key)
			}

			// .hpk/namespace/podName/secretName/*
			podSecretDir, err := h.podDirectory.CreateSubDirectory(vol.Name)
			if err != nil {
				SystemError(err, "cannot create dir '%s' for secrets", podSecretDir)
			}

			for k, v := range secret.Data {
				fullPath := filepath.Join(podSecretDir, k)

				if err := os.WriteFile(fullPath, v, fs.FileMode(*source.DefaultMode)); err != nil {
					SystemError(err, "Could not write secret file %s", fullPath)
				}
			}

		case vol.VolumeSource.EmptyDir != nil:
			/*---------------------------------------------------
			 * EmptyDir
			 *---------------------------------------------------*/
			emptyDir, err := h.podDirectory.CreateSubDirectory(vol.Name)
			if err != nil {
				SystemError(err, "cannot create dir '%s' for emptyDir", emptyDir)
			}
			// without size limit for now

		case vol.VolumeSource.DownwardAPI != nil:
			/*---------------------------------------------------
			 * Downward API
			 *---------------------------------------------------*/
			downApiDir, err := h.podDirectory.CreateSubDirectory(vol.Name)
			if err != nil {
				SystemError(err, "cannot create dir '%s' for downwardApi", downApiDir)
			}

			for _, item := range vol.DownwardAPI.Items {
				itemPath := filepath.Join(downApiDir, item.Path)
				value, err := fieldpath.ExtractFieldPathAsString(h.Pod, item.FieldRef.FieldPath)
				if err != nil {
					PodError(h.Pod, ReasonSpecError, err.Error())

					return
				}

				if err := os.WriteFile(itemPath, []byte(value), fs.FileMode(*vol.Projected.DefaultMode)); err != nil {
					SystemError(err, "cannot write config map file '%s'", itemPath)
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
					SystemError(err, "cannot create HostPathUnset at path '%s'", path)
				}

			case corev1.HostPathDirectoryOrCreate:
				// If nothing exists at the given path, an empty directory will be created there
				// as needed with file mode 0755, having the same group and ownership with Kubelet.
				if path, err := h.podDirectory.CreateSubDirectory(vol.Name); err != nil {
					SystemError(err, "cannot create HostPathDirectoryOrCreate at path '%s'", path)
				}
			case corev1.HostPathDirectory:
				// A directory must exist at the given path
				if path, err := h.podDirectory.CreateSubDirectory(vol.Name); err != nil {
					SystemError(err, "cannot create HostPathDirectory at path '%s'", path)
				}

			case corev1.HostPathFileOrCreate:
				// If nothing exists at the given path, an empty file will be created there
				// as needed with file mode 0644, having the same group and ownership with Kubelet.
				// .hpk/podName/volName/*
				f, err := os.Create(string(h.podDirectory))
				if err != nil {
					SystemError(err, "cannot create  '%s'", string(h.podDirectory))
				}

				if err := f.Close(); err != nil {
					SystemError(err, "cannot close file '%s'", string(h.podDirectory))
				}
			case corev1.HostPathFile:
				// A file must exist at the given path
				// .hpk/podName/volName/*
				f, err := os.Create(string(h.podDirectory))
				if err != nil {
					SystemError(err, "cannot create '%s'", string(h.podDirectory))
				}

				if err := f.Close(); err != nil {
					SystemError(err, "cannot close file '%s'", string(h.podDirectory))
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
				SystemError(err, "cannot create dir '%s' for projected volume", projectedVolPath)
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
							PodError(h.Pod, ReasonSpecError, err.Error())

							return
						}

						if err := os.WriteFile(itemPath, []byte(value), fs.FileMode(*vol.Projected.DefaultMode)); err != nil {
							SystemError(err, "cannot write config map file '%s'", itemPath)
						}
					}

				case projectedSrc.ServiceAccountToken != nil:
					/*---------------------------------------------------
					 * Projected ServiceAccountToken
					 *---------------------------------------------------*/
					var serviceAccount corev1.ServiceAccount

					key := types.NamespacedName{Namespace: h.Pod.GetNamespace(), Name: h.Pod.Spec.ServiceAccountName}

					if err := compute.K8SClient.Get(ctx, key, &serviceAccount); k8errors.IsNotFound(err) {
						PodError(h.Pod, ReasonObjectNotFound, "serviceaccount '%s'", key)

						return
					} else if err != nil {
						SystemError(err, "error getting serviceaccount '%s'", key)
					}

					/*
						automount follows the instructions of:
						https://kubernetes.io/docs/tasks/configure-pod-container/configure-service-account/
					*/

					automount := true

					switch {
					case h.Pod.Spec.AutomountServiceAccountToken != nil:
						automount = *h.Pod.Spec.AutomountServiceAccountToken
					case serviceAccount.AutomountServiceAccountToken != nil:
						automount = *serviceAccount.AutomountServiceAccountToken
					}

					if automount {
						for _, secretRef := range serviceAccount.Secrets {
							var secret corev1.Secret

							secretKey := types.NamespacedName{Namespace: h.Pod.GetNamespace(), Name: secretRef.Name}

							if err := compute.K8SClient.Get(ctx, secretKey, &secret); k8errors.IsNotFound(err) {
								PodError(h.Pod, ReasonObjectNotFound, "secret '%s'", secretKey)

								return
							} else if err != nil {
								SystemError(err, "error getting secret '%s'", secretKey)
							}

							// TODO: Update upon exceeded expiration date
							// TODO: Ensure that these files are deleted in failure cases
							fullPath := filepath.Join(projectedVolPath, projectedSrc.ServiceAccountToken.Path)

							if err := os.WriteFile(fullPath, secret.Data[projectedSrc.ServiceAccountToken.Path], fs.FileMode(0o766)); err != nil {
								SystemError(err, "cannot write token '%s'", fullPath)
							}
						}
					}

					/*
						source := projectedSrc.ServiceAccountToken

						tokenRequest, err := compute.ClientSet.CoreV1().
							ServiceAccounts(h.Pod.GetNamespace()).
							CreateToken(ctx, h.Pod.Spec.ServiceAccountName, &authv1.TokenRequest{
								Spec: authv1.TokenRequestSpec{
									Audiences:         []string{source.Audience},
									ExpirationSeconds: source.ExpirationSeconds,
									BoundObjectRef: &authv1.BoundObjectReference{
										Kind:       "Pod",
										APIVersion: "v1",
										Name:       h.Pod.GetName(),
									},
								},
							}, metav1.CreateOptions{})

						if err != nil {
							return errors.Wrapf(err, "failed to create token for Pod '%s'", h.podKey)
						}

						// TODO: Update upon exceeded expiration date
						// TODO: Ensure that these files are deleted in failure cases
						fullPath := filepath.Join(projectedVolPath, projectedSrc.ServiceAccountToken.Path)

						if err := os.WriteFile(fullPath, []byte(tokenRequest.Status.Token), fs.FileMode(0o766)); err != nil {
							return errors.Wrapf(err, "cannot write token '%s'", fullPath)
						}


							var automount bool

							switch {
							case h.Pod.Spec.AutomountServiceAccountToken != nil:
								automount = *h.Pod.Spec.AutomountServiceAccountToken
							case serviceAccount.AutomountServiceAccountToken != nil:
								automount = *serviceAccount.AutomountServiceAccountToken
							}

							if automount {
								for _, secretRef := range serviceAccount.Secrets {
									var secret corev1.Secret

									secretKey := types.NamespacedName{Namespace: secretRef.Namespace, Name: secretRef.Name}

									if err := compute.K8SClient.Get(ctx, secretKey, &secret); err != nil {
										return errors.Wrapf(err, "failed to get secret '%s'", secretKey)
									}

									// TODO: Update upon exceeded expiration date
									// TODO: Ensure that these files are deleted in failure cases
									fullPath := filepath.Join(projectedVolPath, projectedSrc.ServiceAccountToken.Path)

									if err := os.WriteFile(fullPath, secret.Data[projectedSrc.ServiceAccountToken.Path], fs.FileMode(0o766)); err != nil {
										return errors.Wrapf(err, "cannot write config map file '%s'", fullPath)
									}
								}
							}
					*/

				case projectedSrc.ConfigMap != nil:
					/*---------------------------------------------------
					 * Projected ConfigMap
					 *---------------------------------------------------*/
					var configmap corev1.ConfigMap

					source := projectedSrc.ConfigMap

					key := types.NamespacedName{Namespace: h.Pod.GetNamespace(), Name: source.Name}

					if err := compute.K8SClient.Get(ctx, key, &configmap); k8errors.IsNotFound(err) {
						if source.Optional != nil && *source.Optional == false {
							PodError(h.Pod, ReasonObjectNotFound, "configmap '%s'", key)
						}

						/*-- configmap is optional, and we can safely skip the step --*/
						return
					} else if err != nil {
						SystemError(err, "error getting configmap '%s'", key)
					}

					for k, item := range configmap.Data {
						// TODO: Ensure that these files are deleted in failure cases
						itemPath := filepath.Join(projectedVolPath, k)
						if err := os.WriteFile(itemPath, []byte(item), PodGlobalDirectoryPermissions); err != nil {
							SystemError(err, "cannot write config map file '%s'", itemPath)
						}
					}

				case projectedSrc.Secret != nil:
					/*---------------------------------------------------
					 * Projected Secret
					 *---------------------------------------------------*/
					var secret corev1.Secret

					source := projectedSrc.Secret

					key := types.NamespacedName{Namespace: h.Pod.GetNamespace(), Name: source.Name}

					if err := compute.K8SClient.Get(ctx, key, &secret); k8errors.IsNotFound(err) {
						if source.Optional != nil && *source.Optional == false {
							PodError(h.Pod, ReasonObjectNotFound, "secret '%s'", key)
						}

						/*-- secret is optional, and we can safely skip the step --*/
						return
					} else if err != nil {
						SystemError(err, "error getting secret '%s'", key)
					}

					for k, item := range secret.Data {
						// TODO: Ensure that these files are deleted in failure cases
						itemPath := filepath.Join(projectedVolPath, k)

						if err := os.WriteFile(itemPath, item, PodGlobalDirectoryPermissions); err != nil {
							SystemError(err, "cannot write config map file '%s'", itemPath)
						}
					}
				default:
					logrus.Warn(projectedSrc)

					panic("Have I missed something ")
				}
			}
		}
	}
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
