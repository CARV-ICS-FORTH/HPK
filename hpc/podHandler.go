package hpc

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/carv-ics-forth/knoc/pkg/manager"
	"github.com/carv-ics-forth/knoc/pkg/utils"
	"github.com/go-logr/logr"
	"github.com/hashicorp/go-multierror"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/carv-ics-forth/knoc/api"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/json"
)

var escapeScripts = regexp.MustCompile(`(--[A-Za-z0-9\-]+=)(\$[{\(][A-Za-z0-9_]+[\)\}])`)

var defaultLogger = zap.New(zap.UseDevMode(true))

type podHandler struct {
	*corev1.Pod

	ResourceManager   *manager.ResourceManager
	ContainerRegistry string

	PauseContainerPID int

	podKey       api.ObjectKey
	podDirectory string
	logger       logr.Logger
}

func GetPod(ctx context.Context, podRef api.ObjectKey) (*corev1.Pod, error) {
	encodedPodFilepath := PodSpecFilePath(podRef)

	encodedPod, err := os.ReadFile(encodedPodFilepath)
	if err != nil {
		// if not found, it means that Pod that:
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

func GetPods(ctx context.Context) ([]*corev1.Pod, error) {
	var pods []*corev1.Pod
	var merr *multierror.Error

	/*---------------------------------------------------
	 * Iterate the filesystem and extract local pods
	 *---------------------------------------------------*/
	filepath.Walk(api.RuntimeDir, func(path string, info os.FileInfo, err error) error {
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

func SavePod(ctx context.Context, pod *corev1.Pod) error {
	podKey := api.ObjectKeyFromObject(pod)
	logger := defaultLogger.WithValues("pod", podKey)

	encodedPod, err := json.Marshal(pod)
	if err != nil {
		return errors.Wrapf(err, "cannot marshall pod '%s' to json", podKey)
	}

	encodedPodFile := PodSpecFilePath(podKey)

	if err := os.WriteFile(encodedPodFile, encodedPod, api.PodSpecJsonFilePermissions); err != nil {
		return errors.Wrapf(err, "cannot write pod json file '%s'", encodedPodFile)
	}

	logger.Info("DISK <-- Event: Pod Status Renewed",
		"version", pod.ResourceVersion,
		"phase", pod.Status.Phase,
	)

	return nil
}

func DeletePod(ctx context.Context, pod *corev1.Pod) error {
	/*
		DeletePod takes a Kubernetes Pod and deletes it from the provider.
		TODO: Once a pod is deleted, the provider is expected
		to call the NotifyPods callback with a terminal pod status
		where all the containers are in a terminal state, as well as the pod.
		DeletePod may be called multiple times for the same pod
	*/

	for i := range pod.Status.InitContainerStatuses {
		pod.Status.InitContainerStatuses[i].State.Terminated = &corev1.ContainerStateTerminated{
			Reason:     "PodIsDeleted",
			Message:    "Pod is being deleted",
			FinishedAt: metav1.Now(),
		}
	}

	for i := range pod.Status.ContainerStatuses {
		pod.Status.ContainerStatuses[i].State.Terminated = &corev1.ContainerStateTerminated{
			Reason:     "PodIsDeleted",
			Message:    "Pod is being deleted",
			FinishedAt: metav1.Now(),
		}
	}

	podDir := RuntimeDir(api.ObjectKeyFromObject(pod))

	if err := os.RemoveAll(podDir); err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.Wrapf(err, "failed to delete pod directory %s'", podDir)
	}

	return nil
}

func CreatePod(ctx context.Context, pod *corev1.Pod, rmanager *manager.ResourceManager, registry string) error {
	podKey := api.ObjectKeyFromObject(pod)
	logger := defaultLogger.WithValues("pod", podKey)

	/*---------------------------------------------------
	 * Prepare Pod Environment & handler
	 *---------------------------------------------------*/
	logger.Info("== Creating Pod ==")

	// create pod's top-level and internal sbatch directories
	sbatchDir := SBatchDirectory(podKey)
	if err := os.MkdirAll(sbatchDir, api.PodGlobalDirectoryPermissions); err != nil {
		return errors.Wrapf(err, "Cant create sbatch directory '%s'", sbatchDir)
	}

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
	 * Kind of Journaling for Pod Creation on Slurm
	 *---------------------------------------------------*/
	if err := SavePod(ctx, pod); err != nil {
		return errors.Wrapf(err, "failed to save pod description")
	}

	h := podHandler{
		Pod:               pod,
		ResourceManager:   rmanager,
		ContainerRegistry: registry,
		PauseContainerPID: 0,
		podKey:            podKey,
		podDirectory:      RuntimeDir(podKey),
		logger:            logger,
	}

	/*---------------------------------------------------
	 * Prepare Pod Volumes
	 *---------------------------------------------------*/
	logger.Info(" * Creating backend volumes for Pod")

	if err := h.createBackendVolumes(); err != nil {
		return errors.Wrapf(err, "failed to prepare runtime dir for pod '%s'", h.podKey)
	}

	/*---------------------------------------------------
	 * Watch Pod Directory for Changes
	 *---------------------------------------------------*/
	logger.Info(" * Start watching Pod directory", "path", h.podDirectory)

	// because fswatch does not work recursively, we cannot have the container directories nested within the pod.
	// instead, we use a flat directory in the format "podir/containername.{jid,stdout,stdour,...}"
	if err := fswatcher.Add(h.podDirectory); err != nil {
		return errors.Wrapf(err, "register to fsnotify has failed for path '%s'", h.podDirectory)
	}

	/*---------------------------------------------------
	 * Submit Container Creation Requests
	 *---------------------------------------------------*/
	lastInitContainerJobID := -1
	lastContainerJobID := -1

	logger.Info(" * Submit creation requests for init containers")

	for i, initContainer := range pod.Spec.InitContainers {
		if err := h.submitContainerCreationRequest(ctx, &pod.Spec.InitContainers[i], &lastInitContainerJobID); err != nil {
			return errors.Wrapf(err, "creation request failed for init-container '%s'", initContainer.Name)
		}
	}

	logger.Info(" * Submit creation requests for containers")

	for i, container := range h.Pod.Spec.Containers {
		if err := h.submitContainerCreationRequest(ctx, &pod.Spec.Containers[i], &lastContainerJobID); err != nil {
			return errors.Wrapf(err, "creation request failed for container '%s'", container.Name)
		}
	}

	logger.Info(" * All container requests have been successfully submitted")

	return nil
}

func (h *podHandler) submitContainerCreationRequest(ctx context.Context, container *corev1.Container, prevJobID *int) error {
	h.logger.Info("== New Container Creation Request  ===", "container", container.Name)

	/*---------------------------------------------------
	 * Prepare flags and arguments for Singularity Command
	 *---------------------------------------------------*/
	h.logger.Info(" * Set flags and args for singularity command")

	setEnvOptions := func(container *corev1.Container) []string {
		envArgs := make([]string, 0, len(container.Env))

		for _, envVar := range container.Env {
			envArgs = append(envArgs, envVar.Name+"="+envVar.Value)
		}

		return []string{"--env", strings.Join(envArgs, ",")}
	}

	setBindOptions := func(container *corev1.Container) []string {
		mountArgs := make([]string, 0, len(container.VolumeMounts))

		for _, mountVar := range container.VolumeMounts {
			mountArgs = append(mountArgs, MountPaths(h.podKey, mountVar))
		}

		return []string{"--bind", strings.Join(mountArgs, ",")}
	}

	setExec := func(flags []string) []string {
		// TODO: @malvag, explain yourself.
		cleanedFlagValues := make([]string, 0, len(flags))

		for _, flag := range flags {
			flagValurWithEvaluationPending := escapeScripts.FindStringSubmatch(flag)
			if len(flagValurWithEvaluationPending) > 0 {
				param := flagValurWithEvaluationPending[1]
				value := flagValurWithEvaluationPending[2]
				cleanedFlagValues = append(cleanedFlagValues, param+"'"+value+"'")
			} else {
				cleanedFlagValues = append(cleanedFlagValues, flag)
			}
		}

		return cleanedFlagValues
	}

	/*---------------------------------------------------
	 * Build the Singularity Command
	 *---------------------------------------------------*/
	h.logger.Info(" * Finalize Singularity Command")

	var singularityCommand []string
	{
		if h.PauseContainerPID != 0 {
			//nolint:lll    // panic(errors.Wrapf(nil, "Invalid pauseContainer's PID; can't proceed without an already established network namespace."))
			singularityCommand = append(singularityCommand, []string{
				"nsenter", "-t", string(rune(h.PauseContainerPID)), "-n",
			}...)
		}

		singularityCommand = append(singularityCommand, Executables.SingularityPath(), "exec") // <---- singularity exec
		singularityCommand = append(singularityCommand, setEnvOptions(container)...)           // <---- Options
		singularityCommand = append(singularityCommand, setBindOptions(container)...)          // <---- ...
		singularityCommand = append(singularityCommand, h.ContainerRegistry+container.Image)   // <---- Image
		singularityCommand = append(singularityCommand, setExec(container.Command)...)         // <---- Execution
		singularityCommand = append(singularityCommand, setExec(container.Args)...)            // <---- ...
	}

	/*---------------------------------------------------
	 * Submit the singularity command to Slurm
	 *---------------------------------------------------*/
	jobID, err := h.submitSlurmJob(ctx, container, *prevJobID, singularityCommand)
	if err != nil {
		return errors.Wrap(err, "slurm error")
	}

	h.logger.Info(" * Job has been submitted to Slurm", "jobID", jobID)

	return nil
}

func (h *podHandler) submitSlurmJob(ctx context.Context, container *corev1.Container, prevJobID int, singularityCommand []string) (int, error) {
	/*---------------------------------------------------
	 * Generate SBatch script
	 *---------------------------------------------------*/
	var sbatchFlagsAsString string
	{
		//  parse sbatch flags from Argo
		if slurmFlags, ok := h.Pod.GetAnnotations()["slurm-job.knoc.io/flags"]; ok {
			for _, slurmFlag := range strings.Split(slurmFlags, " ") {
				sbatchFlagsAsString += "\n#SBATCH " + slurmFlag
			}
		}

		// SLURM_JOB_DEPENDENCY ensures sequential execution of init containers and main containers
		if prevJobID != -1 {
			sbatchFlagsAsString += "\n#SBATCH --dependency afterok:" + string(rune(prevJobID))
		}

		// append mpi-flags to singularity
		if mpiFlags, ok := h.Pod.GetAnnotations()["slurm-job.knoc.io/mpi-flags"]; ok {
			if mpiFlags != "true" {
				mpi := append([]string{Executables.MpiexecPath(), "-np", "$SLURM_NTASKS"}, strings.Split(mpiFlags, " ")...)
				singularityCommand = append(mpi, singularityCommand...)
			}
		}
	}

	/*---------------------------------------------------
	 * Prepare environment to receive SBatch results
	 *---------------------------------------------------*/
	scriptFile := SBatchFilePath(h.podKey, container.Name)
	stdoutPath := StdOutputFilePath(h.podKey, container.Name)
	stderrPath := StdErrorFilePath(h.podKey, container.Name)
	exitCodePath := ExitCodeFilePath(h.podKey, container.Name)

	jobName := h.Pod.GetName() + "/" + container.Name

	finalScriptData := Executables.SbatchMacros(jobName, sbatchFlagsAsString) + "\n" +
		strings.Join(singularityCommand, " ") + " >> " + stdoutPath +
		" 2>> " + stderrPath +
		"\n echo $? > " + exitCodePath

	/*---------------------------------------------------
	 * Submit SBatch to Slurm
	 *---------------------------------------------------*/
	if err := os.WriteFile(scriptFile, []byte(finalScriptData), api.ContainerJobPermissions); err != nil {
		return -1, errors.Wrapf(err, "unable to write sbatch script in file '%s'", scriptFile)
	}

	out, err := Executables.SBatchFromFile(ctx, scriptFile)
	if err != nil {
		h.logger.Error(err, "sbatch submission error", "out", out)

		return -1, errors.Wrapf(err, "sbatch submission error")
	}

	/*---------------------------------------------------
	 * Parse Submission Results
	 *---------------------------------------------------*/
	r := regexp.MustCompile(`Submitted batch job (?P<jid>\d+)`)
	jid := r.FindStringSubmatch(out)

	intJobID, err := strconv.Atoi(jid[1])
	if err != nil {
		return -1, errors.Wrap(err, "Cant convert jid as integer. Parsed irregular output!")
	}

	// persist job id
	jobIDPath := JobIDFilePath(h.podKey, container.Name)

	if err := os.WriteFile(jobIDPath, []byte(jid[1]), api.ContainerJobPermissions); err != nil {
		return -1, errors.Wrapf(err, "unable to persist Slurm Job ID")
	}

	return intJobID, nil
}

func (h *podHandler) createBackendVolumes() error {
	pod := h.Pod

	/*---------------------------------------------------
	 * Copy volumes from local Pod to remote Environment
	 *---------------------------------------------------*/
	for _, vol := range pod.Spec.Volumes {
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

			configMap, err := h.ResourceManager.GetConfigMap(source.Name, pod.GetNamespace())
			if k8serrors.IsNotFound(err) {
				if source.Optional != nil && !*source.Optional {
					return errors.Wrapf(err, "Configmap '%s' is required by Pod '%s' and does not exist", source.Name, pod.GetName())
				}
			}

			if err != nil {
				return errors.Wrapf(err, "Error getting configmap '%s' from API server", pod.Name)
			}

			// .knoc/namespace/podName/volName/*
			podConfigMapDir, err := createSubDirectory(h.podDirectory, vol.Name)
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

			secret, err := h.ResourceManager.GetSecret(source.SecretName, pod.Namespace)
			if k8serrors.IsNotFound(err) {
				if source.Optional != nil && !*source.Optional {
					return errors.Wrapf(err, "Secret '%s' is required by Pod '%s' and does not exist", source.SecretName, pod.GetName())
				}
			}

			if err != nil {
				return errors.Wrapf(err, "Error getting secret '%s' from API server", pod.Name)
			}

			// .knoc/namespace/podName/secretName/*
			podSecretDir, err := createSubDirectory(h.podDirectory, vol.Name)
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
			emptyDir, err := createSubDirectory(h.podDirectory, vol.Name)
			if err != nil {
				return errors.Wrapf(err, "cannot create dir '%s' for emptyDir", emptyDir)
			}
			// without size limit for now

		case vol.VolumeSource.DownwardAPI != nil:
			/*---------------------------------------------------
			 * Downward API
			 *---------------------------------------------------*/
			downApiDir, err := createSubDirectory(h.podDirectory, vol.Name)
			if err != nil {
				return errors.Wrapf(err, "cannot create dir '%s' for downwardApi", downApiDir)
			}

			for _, item := range vol.DownwardAPI.Items {
				itemPath := filepath.Join(downApiDir, item.Path)
				value, err := utils.ExtractFieldPathAsString(pod, item.FieldRef.FieldPath)
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
			hostPathVolPath := filepath.Join(h.podDirectory, vol.Name)

			switch *vol.VolumeSource.HostPath.Type {
			case corev1.HostPathUnset:
				// For backwards compatible, leave it empty if unset
				// .hpk/podName/volName/*
				if err := os.MkdirAll(hostPathVolPath, api.PodGlobalDirectoryPermissions); err != nil {
					return errors.Wrapf(err, "cannot create '%s'", hostPathVolPath)
				}
			case corev1.HostPathDirectoryOrCreate:
				// If nothing exists at the given path, an empty directory will be created there
				// as needed with file mode 0755, having the same group and ownership with Kubelet.

				// .hpk/podName/volName/*
				if err := os.MkdirAll(hostPathVolPath, api.PodGlobalDirectoryPermissions); err != nil {
					return errors.Wrapf(err, "cannot create '%s'", hostPathVolPath)
				}
			case corev1.HostPathDirectory:
				// A directory must exist at the given path
				// .hpk/podName/volName/*
				if err := os.MkdirAll(hostPathVolPath, api.PodGlobalDirectoryPermissions); err != nil {
					return errors.Wrapf(err, "cannot create '%s'", hostPathVolPath)
				}

			case corev1.HostPathFileOrCreate:
				// If nothing exists at the given path, an empty file will be created there
				// as needed with file mode 0644, having the same group and ownership with Kubelet.
				// .hpk/podName/volName/*
				f, err := os.Create(hostPathVolPath)
				if err != nil {
					return errors.Wrapf(err, "cannot create '%s'", hostPathVolPath)
				}

				if err := f.Close(); err != nil {
					return errors.Wrapf(err, "cannot close file '%s'", hostPathVolPath)
				}
			case corev1.HostPathFile:
				// A file must exist at the given path
				// .hpk/podName/volName/*
				f, err := os.Create(hostPathVolPath)
				if err != nil {
					return errors.Wrapf(err, "cannot create '%s'", hostPathVolPath)
				}

				if err := f.Close(); err != nil {
					return errors.Wrapf(err, "cannot close file '%s'", hostPathVolPath)
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
			projectedVolPath, err := createSubDirectory(h.podDirectory, vol.Name)
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

						value, err := utils.ExtractFieldPathAsString(pod, item.FieldRef.FieldPath)
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
					serviceAccount, err := h.ResourceManager.GetServiceAccount(pod.Spec.ServiceAccountName, pod.GetNamespace())
					if err != nil {
						return errors.Wrapf(err, "Error getting configmap '%s' from API server", pod.Name)
					}

					if serviceAccount == nil {
						panic("this should never happen ")
					}

					if serviceAccount.AutomountServiceAccountToken != nil && *serviceAccount.AutomountServiceAccountToken {
						for _, secretRef := range serviceAccount.Secrets {
							secret, err := h.ResourceManager.GetSecret(secretRef.Name, pod.GetNamespace())
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
					configMap, err := h.ResourceManager.GetConfigMap(projectedSrc.ConfigMap.Name, pod.GetNamespace())
					{ // err check
						if projectedSrc.ConfigMap.Optional != nil && !*projectedSrc.ConfigMap.Optional {
							return errors.Wrapf(err, "Configmap '%s' is required by Pod '%s' and does not exist", projectedSrc.ConfigMap.Name, pod.GetName())
						}

						if err != nil {
							return errors.Wrapf(err, "Error getting configmap '%s' from API server", pod.Name)
						}

						if configMap == nil {
							continue
						}
					}

					for k, item := range configMap.Data {
						// TODO: Ensure that these files are deleted in failure cases
						itemPath := filepath.Join(projectedVolPath, k)
						if err := os.WriteFile(itemPath, []byte(item), api.PodGlobalDirectoryPermissions); err != nil {
							return errors.Wrapf(err, "cannot write config map file '%s'", itemPath)
						}
					}

				case projectedSrc.Secret != nil:
					/*---------------------------------------------------
					 * Projected Secret
					 *---------------------------------------------------*/
					secret, err := h.ResourceManager.GetSecret(projectedSrc.Secret.Name, pod.GetNamespace())
					{ // err check
						if projectedSrc.Secret.Optional != nil && !*projectedSrc.Secret.Optional {
							return errors.Wrapf(err, "Secret '%s' is required by Pod '%s' and does not exist", projectedSrc.Secret.Name, pod.GetName())
						}

						if err != nil {
							return errors.Wrapf(err, "Error getting secret '%s' from API server", pod.Name)
						}

						if secret == nil {
							continue
						}
					}

					for k, item := range secret.Data {
						// TODO: Ensure that these files are deleted in failure cases
						itemPath := filepath.Join(projectedVolPath, k)
						if err := os.WriteFile(itemPath, item, api.PodGlobalDirectoryPermissions); err != nil {
							return errors.Wrapf(err, "cannot write config map file '%s'", itemPath)
						}
					}
				}
			}
		}
	}

	return nil
}
