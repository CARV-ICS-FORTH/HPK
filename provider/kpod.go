package provider

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/carv-ics-forth/knoc/hpc"
	"github.com/carv-ics-forth/knoc/provider/paths"
	"github.com/hashicorp/go-multierror"

	"github.com/carv-ics-forth/knoc/api"
	"github.com/fsnotify/fsnotify"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/json"
)

var escapeScripts = regexp.MustCompile(`(--[A-Za-z0-9\-]+=)(\$[{\(][A-Za-z0-9_]+[\)\}])`)

var errNotFound = errors.New("pod is not found")

func LoadKPod(provider *Provider, podRef api.ObjectKey, cRg string) (*KPod, error) {
	var pod corev1.Pod

	pod.Namespace = podRef.Namespace
	pod.Name = podRef.Name

	podSpecFilePath := paths.PodSpecFilePath(podRef)

	specEnc, err := os.ReadFile(podSpecFilePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, errNotFound
	} else if err != nil {
		return nil, errors.Wrapf(err, "cannot read pod json file '%s'", podSpecFilePath)
	}

	if err := json.Unmarshal(specEnc, &pod); err != nil {
		return nil, errors.Wrapf(err, "failed to decode pod from '%s'", podSpecFilePath)
	}

	return NewKPod(provider, &pod, cRg)
}

func NewKPod(provider *Provider, pod *corev1.Pod, cRg string) (*KPod, error) {

	kpod := &KPod{
		Provider:          provider,
		Pod:               pod,
		Envs:              nil,
		Mounts:            nil,
		Commands:          nil,
		CommandArguments:  nil,
		ContainerRegistry: cRg,
	}

	// create temporary dir
	podRef := api.ObjectKeyFromObject(pod)

	tempDirPath := paths.TemporaryDir(podRef)

	if err := os.MkdirAll(tempDirPath, os.ModePerm); err != nil {
		return nil, errors.Wrapf(err, "Cant create pod directory '%s'", tempDirPath)
	}

	// create runtime dir
	if err := CreateBackendVolumes(provider, kpod); err != nil {
		return nil, errors.Wrapf(err, "Couldn not prepare pod's environment ")
	}

	return kpod, nil
}

type KPod struct {
	*Provider

	Pod *corev1.Pod

	PauseContainerPID int

	Envs             []string
	Mounts           []string
	Commands         []string
	CommandArguments []string

	ContainerRegistry string
}

func (kpod *KPod) CreatePod(ctx context.Context, pod *corev1.Pod) error {
	// return nil
	var prevJobID *int
	prevJobID = new(int)
	*prevJobID = -1
	lastInitContainerJobID := -1

	// // create a dummy container that will be a placeholder of network namespaces
	// FIXME: not currently supported
	// if err := kpod.CreatePauseContainer(); err != nil {
	//	panic(errors.Wrapf(err, "Could not create pause Container"))
	// }

	for index := range pod.Spec.InitContainers {
		if err := kpod.createContainer(ctx, &pod.Spec.InitContainers[index], prevJobID); err != nil {
			return errors.Wrapf(err, "Could not create container from door")
		}

		// We're keeping the last init container jobID to give it to the main Containers as a dependency,
		// since we don't want sequential execution on main Containers
		lastInitContainerJobID = *prevJobID

		// register a status placeholder for the created container
		pod.Status.InitContainerStatuses = append(pod.Status.InitContainerStatuses, corev1.ContainerStatus{
			Name:                 pod.Spec.InitContainers[index].Name,
			State:                corev1.ContainerState{},
			LastTerminationState: corev1.ContainerState{},
			Ready:                false,
			RestartCount:         0,
			Image:                pod.Spec.InitContainers[index].Image,
			ImageID:              "", // TODO: add what ?
			ContainerID:          "", // TODO: add the underlying fs path to container
			Started:              nil,
		})
	}

	for index := range pod.Spec.Containers {
		if err := kpod.createContainer(ctx, &pod.Spec.Containers[index], &lastInitContainerJobID); err != nil {
			return errors.Wrapf(err, "Could not create container from door")
		}

		// register a status placeholder for the created container
		pod.Status.ContainerStatuses = append(pod.Status.ContainerStatuses, corev1.ContainerStatus{
			Name:                 pod.Spec.Containers[index].Name,
			State:                corev1.ContainerState{},
			LastTerminationState: corev1.ContainerState{},
			Ready:                false,
			RestartCount:         0,
			Image:                pod.Spec.Containers[index].Image,
			ImageID:              "", // TODO: add what ?
			ContainerID:          "", // TODO: add the underlying fs path to container
			Started:              nil,
		})
	}

	// set the ip of the running pods to the virtual node
	pod.Status.PodIP = kpod.Provider.InternalIP
	pod.Status.PodIPs = []corev1.PodIP{{
		IP: kpod.Provider.InternalIP,
	}}

	return nil
}

func (kpod *KPod) createContainer(ctx context.Context, container *corev1.Container, prevJobID *int) error {
	logrus.Info("Create container")
	/* Prepare the data environment */
	if err := kpod.PrepareContainerData(container); err != nil {
		return errors.Wrap(err, "cannot prepare data environment")
	}

	/*
		local singularity image files currently unavailable
	*/
	/*
		if strings.HasPrefix(c.Image, "/") {

			if imageURI, ok := c.GetObjectMeta().GetAnnotations()["slurm-job.knoc.io/image-root"]; ok {
				logrus.Debugln(imageURI)
				image = imageURI + c.Image
			} else {
				return errors.Errorf("image-uri annotation not specified for path in remote filesystem")
			}
		}
	*/
	var singularityCommand []string

	// linter calls me out on converting the integer first to rune and then to string... dunno, i'll comply
	if kpod.PauseContainerPID != 0 {
		//nolint:lll    // panic(errors.Wrapf(nil, "Invalid pauseContainer's PID; can't proceed without an already established network namespace."))
		singularityCommand = append(singularityCommand, []string{
			"nsenter", "-t", string(rune(kpod.PauseContainerPID)), "-n",
		}...)
	}

	singularityExec := []string{
		kpod.Provider.HPC.SingularityPath(), "exec",
	}

	singularityCommand = append(singularityCommand, singularityExec...)
	singularityCommand = append(singularityCommand, kpod.Envs...)
	singularityCommand = append(singularityCommand, kpod.Mounts...)
	singularityCommand = append(singularityCommand, kpod.ContainerRegistry+container.Image)
	singularityCommand = append(singularityCommand, kpod.Commands...)
	singularityCommand = append(singularityCommand, kpod.CommandArguments...)

	path, err := kpod.produceSlurmScript(container, *prevJobID, singularityCommand)
	if err != nil {
		return errors.Wrap(err, "cannot generate slurm script")
	}

	out, err := kpod.Provider.HPC.SBatchFromFile(path)
	if err != nil {
		return errors.Wrapf(err, "Can't submit sbatch script")
	}

	// if sbatch script is successfully submitted check for job id
	jid, err := kpod.handleJobID(container, out)
	if err != nil {
		return errors.Wrapf(err, "handleJobID")
	}

	*prevJobID = jid

	return kpod.WatchPodDirectory(ctx)
}

func (kpod *KPod) PrepareContainerData(container *corev1.Container) error {
	preparePendingEvaluations := func(flags []string) []string {
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

	prepareEnvironment := func(container *corev1.Container) []string {
		envArgs := make([]string, 0, len(container.Env))

		for _, envVar := range container.Env {
			envArgs = append(envArgs, envVar.Name+"="+envVar.Value)
		}

		// in case there is an evaluation pending inside the container arguments
		return []string{"--env", strings.Join(envArgs, ",")}
	}

	kpod.Envs = prepareEnvironment(container)
	kpod.Commands = preparePendingEvaluations(container.Command)
	kpod.CommandArguments = preparePendingEvaluations(container.Args)
	kpod.Mounts = kpod.prepareMounts(container)

	return nil
}

func (kpod *KPod) prepareMounts(container *corev1.Container) []string {
	mountArgs := make([]string, 0, len(container.VolumeMounts))

	podRef := api.ObjectKeyFromObject(kpod.Pod)

	for _, mountVar := range container.VolumeMounts {
		mountArgs = append(mountArgs, paths.GenerateMountPaths(podRef, mountVar))
	}

	return []string{"--bind", strings.Join(mountArgs, ",")}
}

func (kpod *KPod) produceSlurmScript(container *corev1.Container, prevJobID int, command []string) (string, error) {
	podRef := api.ObjectKeyFromObject(kpod.Pod)

	sbatchRelativePath := paths.ScriptFilePath(podRef, container.Name)

	var sbatchFlagsFromArgo []string
	sbatchFlagsAsString := ""

	tmpDir := paths.TemporaryDir(podRef)

	if err := os.MkdirAll(tmpDir, api.TemporaryPodDataPermissions); err != nil {
		return "", errors.Wrapf(err, "Could not create '%s' directory", tmpDir)
	}

	if slurmFlags, ok := kpod.Pod.GetAnnotations()["slurm-job.knoc.io/flags"]; ok {
		sbatchFlagsFromArgo = strings.Split(slurmFlags, " ")
	}

	for _, slurmFlag := range sbatchFlagsFromArgo {
		sbatchFlagsAsString += "\n#SBATCH " + slurmFlag
	}
	// SLURM_JOB_DEPENDENCY ensures sequential execution of init containers and main containers
	if prevJobID != -1 {
		sbatchFlagsAsString += "\n#SBATCH --dependency afterok:" + string(rune(prevJobID))
	}

	if mpiFlags, ok := kpod.Pod.GetAnnotations()["slurm-job.knoc.io/mpi-flags"]; ok {
		if mpiFlags != "true" {
			mpi := append([]string{kpod.Provider.HPC.MpiexecPath(), "-np", "$SLURM_NTASKS"}, strings.Split(mpiFlags, " ")...)
			command = append(mpi, command...)
		}
	}

	if err := kpod.writeSlurmScriptToFile(sbatchFlagsAsString, container, command); err != nil {
		return "", errors.Wrap(err, "Can't produce sbatch script in file")
	}

	return sbatchRelativePath, nil
}

func (kpod *KPod) writeSlurmScriptToFile(sbatchFlagsAsString string, container *corev1.Container, commandArray []string) error {
	podRef := api.ObjectKeyFromObject(kpod.Pod)

	scriptFile := paths.ScriptFilePath(podRef, container.Name)
	stdoutPath := paths.StdOutputFilePath(podRef, container.Name)
	stderrPath := paths.StdErrorFilePath(podRef, container.Name)
	exitCodePath := paths.ExitCodeFilePath(podRef, container.Name)

	finalScriptData := kpod.Provider.HPC.SbatchMacros(paths.JobName(podRef, container), sbatchFlagsAsString) + "\n" +
		strings.Join(commandArray, " ") + " >> " + stdoutPath +
		" 2>> " + stderrPath +
		"\n echo $? > " + exitCodePath

	logrus.Info("Writing slurm script at '%s'", scriptFile)

	return os.WriteFile(scriptFile, []byte(finalScriptData), 777)
}

func (kpod *KPod) handleJobID(container *corev1.Container, output string) (int, error) {
	r := regexp.MustCompile(`Submitted batch job (?P<jid>\d+)`)
	jid := r.FindStringSubmatch(output)

	intJobID, err := strconv.Atoi(jid[1])
	if err != nil {
		return 0, errors.Wrap(err, "Cant convert jid as integer. Parsed irregular output!")
	}

	podRef := api.ObjectKeyFromObject(kpod.Pod)
	jobIDPath := paths.JobIDFilePath(podRef, container.Name)

	logrus.Info("Writing jid file at %s", jobIDPath)

	return intJobID, os.WriteFile(jobIDPath, []byte(jid[1]), 777)
}

func (kpod *KPod) WatchPodDirectory(ctx context.Context) error {
	// Create new watcher.
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return errors.Wrap(err, "cannot create a new watcher")
	}

	// Add a path.
	if err := watcher.Add(paths.RuntimeDir(api.ObjectKeyFromObject(kpod.Pod))); err != nil {
		return errors.Wrap(err, "add path to watcher has failed")
	}

	// Start listening for events.
	go func() {
		defer watcher.Close()

		for {
			select {
			case <-ctx.Done():
				return

			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				// add event to queue to be processed later
				if err := kpod.Provider.HPC.FSEventDispatcher.Push(event); err != nil {
					panic(err)
				}

			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}

				// TODO: I should handle better the errors from watchers
				log.Println("error:", err)
			}
		}
	}()

	return nil
}

func (kpod *KPod) Save() error {
	marshalledPod, err := json.Marshal(kpod.Pod)
	if err != nil {
		return errors.Wrapf(err, "cannot marhsall pod '%s' to json", kpod.Pod.GetName())
	}

	if err := os.WriteFile(paths.PodSpecFilePath(api.ObjectKeyFromObject(kpod.Pod)), marshalledPod, api.PodSpecJsonFilePermissions); err != nil {
		return errors.Wrapf(err, "cannot write pod json file '%s'", paths.PodSpecFilePath(api.ObjectKeyFromObject(kpod.Pod)))
	}

	return nil
}

func (kpod *KPod) CreateSubDirectory(name string) (string, error) {
	fullPath := filepath.Join(paths.RuntimeDir(api.ObjectKeyFromObject(kpod.Pod)), name)

	if err := os.MkdirAll(fullPath, api.PodGlobalDirectoryPermissions); err != nil {
		return fullPath, errors.Wrapf(err, "cannot create dir '%s'", fullPath)
	}

	return fullPath, nil
}

func (kpod *KPod) generatePauseContainerName() string {
	return filepath.Join(kpod.Pod.GetNamespace(), kpod.Pod.GetName(), api.PauseContainerName)
}

func (kpod *KPod) GetPauseContainerPID() (int, error) {
	pauseInstanceName := kpod.generatePauseContainerName()
	pid, err := hpc.GetPauseInstancePID(pauseInstanceName)
	if err != nil {
		return 0, errors.Wrapf(err, "Could not get pauseContainer's PID")
	}

	return pid, nil
}

func (kpod *KPod) CreatePauseContainer() error {
	podRef := api.ObjectKeyFromObject(kpod.Pod)

	container := new(corev1.Container)
	container.Name = api.PauseContainerName
	container.Image = "scratch"
	sbatchFlagsAsString := ""

	singularityCommand := append([]string{kpod.Provider.HPC.SingularityPath(), "instance", "start"}, kpod.ContainerRegistry+container.Image)
	singularityCommand = append(singularityCommand, api.PauseContainerCommand...)

	if err := os.MkdirAll(paths.TemporaryDir(podRef), api.TemporaryPodDataPermissions); err != nil {
		return errors.Wrapf(err, "Could not create '%s' directory", paths.TemporaryDir(podRef))
	}

	ssfPath := paths.ScriptFilePath(podRef, container.Name)

	scriptFile, err := os.Create(ssfPath)
	if err != nil {
		return errors.Wrapf(err, "Cant create slurm_script '%s'", ssfPath)
	}

	finalScriptData := kpod.Provider.HPC.SbatchMacros(paths.JobName(podRef, container), sbatchFlagsAsString) +
		"\n" +
		strings.Join(singularityCommand, " ") + " >> " + paths.StdOutputFilePath(podRef, container.Name) +
		" 2>> " + paths.StdErrorFilePath(podRef, container.Name) +
		"\n echo $? > " + paths.ExitCodeFilePath(podRef, container.Name)

	if _, err = scriptFile.WriteString(finalScriptData); err != nil {
		return errors.Wrapf(err, "Can't write sbatch script in file '%s'", ssfPath)
	}

	if err := scriptFile.Close(); err != nil {
		return errors.Wrap(err, "Close")
	}

	out, err := kpod.Provider.HPC.SBatchFromFile(ssfPath)
	if err != nil {
		return errors.Wrapf(err, "Can't submit sbatch script")
	}

	// if sbatch script is successfully submitted check for job id
	if _, err := kpod.handleJobID(container, out); err != nil {
		return errors.Wrapf(err, "handleJobID")
	}

	pid, err := kpod.GetPauseContainerPID()
	if err != nil {
		return errors.Wrapf(err, "Can't get pauseContainer PID")
	}

	// note the pauseContainer's PID for later use
	// so that the future containers of this pod, join this namespace
	kpod.PauseContainerPID = pid

	return nil
}

func DeletePodDirectory(pod *corev1.Pod) error {
	podRef := api.ObjectKeyFromObject(pod)

	var merr *multierror.Error

	if err := os.RemoveAll(paths.RuntimeDir(podRef)); err != nil && !errors.Is(err, errNotFound) {
		merr = multierror.Append(merr, errors.Wrapf(err, "failed to delete runtime dir for '%s'", pod.GetName()))
	}

	if err := os.RemoveAll(paths.TemporaryDir(podRef)); err != nil && !errors.Is(err, errNotFound) {
		merr = multierror.Append(merr, errors.Wrapf(err, "failed to delete tempdir dir for '%s'", pod.GetName()))
	}

	if merr.ErrorOrNil() != nil {
		return merr.ErrorOrNil()
	}

	return nil
}
