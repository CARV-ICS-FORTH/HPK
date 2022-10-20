package hpc

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/carv-ics-forth/knoc/api"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func renewPodStatus(ctx context.Context, podKey api.ObjectKey) error {
	pod, err := LoadPod(ctx, podKey)
	if err != nil {
		return errors.Wrapf(err, "unable to load pod")
	}

	if err := resolvePodStatus(pod); err != nil {
		return errors.Wrapf(err, "unable to update pod status")
	}

	// save the updated Pod status.
	if err := SavePod(ctx, pod); err != nil {
		return errors.Wrapf(err, "unable to store pod")
	}

	return nil
}

type test struct {
	expression bool
	change     func(status *corev1.PodStatus)
}

func resolvePodStatus(pod *corev1.Pod) error {
	podKey := api.ObjectKeyFromObject(pod)
	logger := defaultLogger.WithValues("pod", podKey)

	/*---------------------------------------------------
	 * Load Container Statuses
	 *---------------------------------------------------*/
	if err := LoadContainerStatuses(pod); err != nil {
		return errors.Wrapf(err, "Cannot update container status for Pod '%s'", podKey)
	}

	/*---------------------------------------------------
	 * Check status of Init Containers
	 *---------------------------------------------------*/
	if pod.Status.Phase == corev1.PodPending {
		logger.Info(" O Checking for status of Init Containers")

		for _, initContainer := range pod.Status.InitContainerStatuses {

			if initContainer.State.Terminated != nil {
				// the Pod is pending is at least one InitContainer is still running
				return nil
			}
		}
	}

	/*---------------------------------------------------
	 * Classify container statuses
	 *---------------------------------------------------*/
	logger.Info(" O Checking for status of Containers Containers")

	var state Classifier
	state.Reset()

	for i, containerStatus := range pod.Status.ContainerStatuses {
		state.Classify(containerStatus.Name, &pod.Status.ContainerStatuses[i])
	}

	totalJobs := len(pod.Spec.Containers)

	/*---------------------------------------------------
	 * Handle Pod lifecycle based on Container Statuses
	 *---------------------------------------------------*/

	testSequence := []test{
		{ /*-- FAILED: at least one job has failed --*/
			expression: state.NumFailedJobs() > 0,
			change: func(status *corev1.PodStatus) {
				status.Phase = corev1.PodFailed
				status.Reason = "ContainerFailed"
				status.Message = fmt.Sprintf("Failed containers: %s", state.ListFailedJobs())
			},
		},

		{ /*-- SUCCESS: all jobs are successfully completed --*/
			expression: state.NumSuccessfulJobs() == totalJobs,
			change: func(status *corev1.PodStatus) {
				status.Phase = corev1.PodSucceeded
			},
		},

		{ /*-- RUNNING: one job is still running --*/
			expression: state.NumRunningJobs()+state.NumSuccessfulJobs() == totalJobs,
			change: func(status *corev1.PodStatus) {
				status.Phase = corev1.PodRunning
			},
		},

		{ /*-- PENDING: some jobs are not yet created --*/
			expression: state.NumPendingJobs() > 0,
			change: func(status *corev1.PodStatus) {
				status.Phase = corev1.PodPending
			},
		},

		{ /*-- FAILED: invalid state transition --*/
			expression: true,
			change: func(status *corev1.PodStatus) {
				status.Phase = corev1.PodFailed
			},
		},
	}

	for _, testcase := range testSequence {
		if testcase.expression { // Check if any lifecycle condition is met
			// update the cached Pod Status.
			testcase.change(&pod.Status)

			return nil
		}
	}

	panic(errors.Errorf(`unhandled lifecycle conditions.
			current: '%v',
			totalJobs: '%d',
			jobs: '%s',
		 `, pod.Status.Phase, totalJobs, state.ListAll()))
}

/*************************************************************

		Load Container status from the FS

*************************************************************/

func LoadContainerStatuses(pod *corev1.Pod) error {
	podKey := api.ObjectKeyFromObject(pod)

	/*---------------------------------------------------
	 * Init Containers
	 *---------------------------------------------------*/
	for i, container := range pod.Spec.InitContainers {
		exitCodePath := ExitCodeFilePath(podKey, container.Name)

		code, exists := readIntFromFile(exitCodePath)
		if !exists {
			/*-- running --*/
			continue
		}

		/*-- terminated --*/
		pod.Status.InitContainerStatuses[i].State.Terminated.ExitCode = int32(code)
	}

	/*---------------------------------------------------
	 * Containers
	 *---------------------------------------------------*/
	for i, container := range pod.Spec.Containers {
		exitCodePath := ExitCodeFilePath(podKey, container.Name)
		jobIDPath := JobIDFilePath(podKey, container.Name)

		code, exists := readIntFromFile(exitCodePath)
		if !exists {
			/*-- running --*/
			continue
		}

		/*-- terminated --*/
		containerID, exists := readStringFromFile(jobIDPath)
		if !exists {
			panic("this should never happen")
		}

		status := &pod.Status.ContainerStatuses[i]

		if status.RestartCount == 0 {
			(*status).State.Terminated = &corev1.ContainerStateTerminated{
				ExitCode:    int32(code),
				Signal:      0,
				Reason:      "",
				Message:     trytoDecodeExitCode(code),
				StartedAt:   metav1.Time{Time: time.Time{}}, // FIXME: should be taken by the fs info
				FinishedAt:  metav1.Time{Time: time.Now()},
				ContainerID: containerID,
			}
		} else {
			(*status).State.Terminated.ExitCode = int32(code)
			(*status).State.Terminated.Message = trytoDecodeExitCode(code)
			(*status).State.Terminated.StartedAt = metav1.Time{Time: time.Time{}} // FIXME: should be taken by the fs info
			(*status).State.Terminated.FinishedAt = metav1.Time{Time: time.Now()}
		}
	}

	return nil
}

func trytoDecodeExitCode(code int) string {
	switch code {
	case 255:
		return "this usually indicates initialization failure"
	default:
		return ""
	}
}

func readStringFromFile(filepath string) (string, bool) {
	out, err := os.ReadFile(filepath)
	if err != nil {
		logrus.Warnf("cannot read file '%s'", filepath)

		return "", false
	}

	return string(out), true
}

func readIntFromFile(filepath string) (int, bool) {
	out, err := os.ReadFile(filepath)
	if err != nil {
		return -1, false
	}

	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	scanner.Split(bufio.ScanWords)

	if scanner.Scan() {
		code, err := strconv.Atoi(scanner.Text())
		if err != nil {
			panic(errors.Wrap(err, "cannot decode content to int"))
		}

		return code, true
	}

	return -1, false
}

/*************************************************************

				Pod Lifecycle

*************************************************************/

// Classifier splits jobs into Pending, Running, Successful, and Failed.
// To relief the garbage collector, we use a embeddable structure that we reset at every reconciliation cycle.
type Classifier struct {
	pendingJobs    map[string]*corev1.ContainerStatus
	runningJobs    map[string]*corev1.ContainerStatus
	successfulJobs map[string]*corev1.ContainerStatus
	failedJobs     map[string]*corev1.ContainerStatus
}

func (in *Classifier) Reset() {
	in.pendingJobs = make(map[string]*corev1.ContainerStatus)
	in.runningJobs = make(map[string]*corev1.ContainerStatus)
	in.successfulJobs = make(map[string]*corev1.ContainerStatus)
	in.failedJobs = make(map[string]*corev1.ContainerStatus)
}

// Classify the object based on the  standard Frisbee lifecycle.
func (in *Classifier) Classify(name string, status *corev1.ContainerStatus) {

	switch {
	case status.State.Terminated != nil:
		if status.State.Terminated.ExitCode == 0 {
			in.successfulJobs[name] = status
		} else {
			in.failedJobs[name] = status
		}
	case status.State.Running != nil:
		in.runningJobs[name] = status
	case status.State.Waiting != nil:
		in.pendingJobs[name] = status
	default:
		// if nothing above, then the container is not yet started.
		in.pendingJobs[name] = status
	}
}

func (in *Classifier) NumPendingJobs() int {
	return len(in.pendingJobs)
}

func (in *Classifier) NumRunningJobs() int {
	return len(in.runningJobs)
}

func (in *Classifier) NumSuccessfulJobs() int {
	return len(in.successfulJobs)
}

func (in *Classifier) NumFailedJobs() int {
	return len(in.failedJobs)
}

func (in *Classifier) NumAll() string {
	return fmt.Sprint(
		"\n * Pending:", in.NumPendingJobs(),
		"\n * Running:", in.NumRunningJobs(),
		"\n * Success:", in.NumSuccessfulJobs(),
		"\n * Failed:", in.NumFailedJobs(),
		"\n",
	)
}

func (in *Classifier) ListPendingJobs() []string {
	list := make([]string, 0, len(in.pendingJobs))

	for jobName := range in.pendingJobs {
		list = append(list, jobName)
	}

	sort.Strings(list)

	return list
}

func (in *Classifier) ListRunningJobs() []string {
	list := make([]string, 0, len(in.runningJobs))

	for jobName := range in.runningJobs {
		list = append(list, jobName)
	}

	sort.Strings(list)

	return list
}

func (in *Classifier) ListSuccessfulJobs() []string {
	list := make([]string, 0, len(in.successfulJobs))

	for jobName := range in.successfulJobs {
		list = append(list, jobName)
	}

	sort.Strings(list)

	return list
}

func (in *Classifier) ListFailedJobs() []string {
	list := make([]string, 0, len(in.failedJobs))

	for jobName := range in.failedJobs {
		list = append(list, jobName)
	}

	sort.Strings(list)

	return list
}

func (in *Classifier) ListAll() string {
	return fmt.Sprint(
		"\n * Pending:", in.ListPendingJobs(),
		"\n * Running:", in.ListRunningJobs(),
		"\n * Success:", in.ListSuccessfulJobs(),
		"\n * Failed:", in.ListFailedJobs(),
		"\n",
	)
}

func (in *Classifier) GetPendingJobs(jobNames ...string) []*corev1.ContainerStatus {
	list := make([]*corev1.ContainerStatus, 0, len(in.pendingJobs))

	if len(jobNames) == 0 {
		// if no job names are defined, return everything
		for _, job := range in.pendingJobs {
			list = append(list, job)
		}
	} else {
		// otherwise, iterate the list
		for _, job := range jobNames {
			j, exists := in.pendingJobs[job]
			if exists {
				list = append(list, j)
			}
		}
	}

	return list
}

func (in *Classifier) GetRunningJobs(jobNames ...string) []*corev1.ContainerStatus {
	list := make([]*corev1.ContainerStatus, 0, len(in.runningJobs))

	if len(jobNames) == 0 {
		// if no job names are defined, return everything
		for _, job := range in.runningJobs {
			list = append(list, job)
		}
	} else {
		// otherwise, iterate the list
		for _, job := range jobNames {
			j, exists := in.runningJobs[job]
			if exists {
				list = append(list, j)
			}
		}
	}

	return list
}

func (in *Classifier) GetSuccessfulJobs(jobNames ...string) []*corev1.ContainerStatus {
	list := make([]*corev1.ContainerStatus, 0, len(in.successfulJobs))

	if len(jobNames) == 0 {
		// if no job names are defined, return everything
		for _, job := range in.successfulJobs {
			list = append(list, job)
		}
	} else {
		// otherwise, iterate the list
		for _, job := range jobNames {
			j, exists := in.successfulJobs[job]
			if exists {
				list = append(list, j)
			}
		}
	}

	return list
}

func (in *Classifier) GetFailedJobs(jobNames ...string) []*corev1.ContainerStatus {
	list := make([]*corev1.ContainerStatus, 0, len(in.failedJobs))

	if len(jobNames) == 0 {
		// if no job names are defined, return everything
		for _, job := range in.failedJobs {
			list = append(list, job)
		}
	} else {
		// otherwise, iterate the list
		for _, job := range jobNames {
			j, exists := in.failedJobs[job]
			if exists {
				list = append(list, j)
			}
		}
	}

	return list
}

/*

func changeContainerStateToRunning(pod *corev1.Pod, container *corev1.Container) {
	pod.Status.ContainerStatuses = append(pod.Status.ContainerStatuses, corev1.ContainerStatus{
		Name:         container.Name,
		Image:        container.Image,
		Ready:        true,
		RestartCount: 1,
		State: corev1.ContainerState{
			Running: &corev1.ContainerStateRunning{
				StartedAt: metav1.Now(),
			},
		},
	})
}


func getContainer(ctx context.Context, dir string, file string, inspect InspectFS) (*corev1.Container, *corev1.ContainerStatus, error) {
	fields := strings.Split(dir, "/")
	namespace := fields[1]
	podName := fields[2]
	containerName := strings.TrimSuffix(file, filepath.Ext(file))

	//	find container with given podName and containerName
	pod, err := inspect.GetPod(ctx, namespace, podName)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "Pod in ru ntime directory was not found for container '%s'", containerName)
	}

	if pod == nil {
		panic("undefined behavior. this should never happen.")
	}


	var (
		container *corev1.Container
		containerStatus *corev1.ContainerStatus
	)

	// find out if the container is initContainer or main container
	for index, initContainer := range pod.Spec.InitContainers {
		if initContainer.Name == containerName {
			container = &pod.Spec.InitContainers[index]
			containerStatus = &pod.Status.InitContainerStatuses[index]
		}
	}

	// find out if the container is initContainer or main container
	for index, mainContainer := range pod.Spec.Containers {
		if mainContainer.Name == containerName {
			container = &pod.Spec.Containers[index]
			containerStatus = &pod.Status.ContainerStatuses[index]
		}
	}

	if container == nil  || containerStatus == nil{
		return nil, nil, errors.Errorf("container not found for container '%s'", containerName)
	}

	return container, containerStatus, nil
}


*/

/*


// TODO: should go to provider
// find container with given podName and containerName
func findPodAndContainerStatus(ctx context.Context, dir string, file string, inspect InspectFS) (*corev1.Pod, *corev1.Container, *corev1.ContainerStatus) {
	fields := strings.Split(dir, "/")
	namespace := fields[1]
	podName := fields[2]
	containerName := strings.TrimSuffix(file, filepath.Ext(file))

	logrus.Warn(namespace)
	logrus.Warn(podName)
	logrus.Warn(containerName)

	//	find container with given podName and containerName
	pod, err := inspect.GetPod(ctx, namespace, podName)

	if err != nil || pod == nil {
		panic(errors.Wrapf(err, "Pod in runtime directory was not found for container '%s'", containerName))
	}

	var targetContainer *corev1.Container
	var targetContainerStatus *corev1.ContainerStatus

	// find out if the container is initContainer or main container
	for index, initContainer := range pod.Spec.InitContainers {
		if initContainer.Name == containerName {
			targetContainerStatus = &pod.Status.InitContainerStatuses[index]
			targetContainer = &pod.Spec.InitContainers[index]
		}
	}

	// find out if the container is initContainer or main container
	for index, mainContainer := range pod.Spec.Containers {
		if mainContainer.Name == containerName {
			targetContainerStatus = &pod.Status.ContainerStatuses[index]
			targetContainer = &pod.Spec.Containers[index]
		}
	}

	if targetContainerStatus == nil {
		panic(errors.Errorf("container status not found for container '%s'", containerName))
	}

	return pod, targetContainer, targetContainerStatus
}

*/

/*
func (e *LocalExecutor) ExecuteOperation(ctx context.Context, mode api.Operation, pak *api.KPod) error {
	switch mode {
	case api.SUBMIT:
		var prevJobID *int
		prevJobID = new(int)
		*prevJobID = -1
		lastInitContainerJobID := -1

		for index := range pak.Pod.Spec.InitContainers {
			if err := e.createContainer(ctx, &pak.Pod.Spec.InitContainers[index], prevJobID, pak); err != nil {
				return errors.Wrapf(err, "Could not create container from door")
			}

			// We're keeping the last init container jobID to give it to the main Containers as a dependency,
			// since we don't want sequential execution on main Containers
			lastInitContainerJobID = *prevJobID
		}

		for index := range pak.Pod.Spec.Containers {
			*prevJobID = lastInitContainerJobID
			if err := e.createContainer(ctx, &pak.Pod.Spec.Containers[index], prevJobID, pak); err != nil {
				return errors.Wrapf(err, "Could not create container from door")
			}
		}
	case api.DELETE:
		/* ... * /
	}

	return nil
}

*/

/*
	func (e *LocalExecutor) createContainer(ctx context.Context, container *corev1.Container, prevJobID *int, pak *api.KPod) error {
		logrus.Info("Create container")
		/* Prepare the data environment * /
		if err := e.PrepareContainerData(pak, container); err != nil {
			return errors.Wrap(err, "cannot prepare data environment")
		}

		/*
			local singularity image files currently unavailable
		* /
		/*
			if strings.HasPrefix(c.Image, "/") {

				if imageURI, ok := c.GetObjectMeta().GetAnnotations()["slurm-job.knoc.io/image-root"]; ok {
					logrus.Debugln(imageURI)
					image = imageURI + c.Image
				} else {
					return errors.Errorf("image-uri annotation not specified for path in remote filesystem")
				}
			}
		* /
		singularityCommand := append([]string{SINGULARITY, "exec"}, pak.Envs...)
		singularityCommand = append(singularityCommand, pak.Mounts...)
		singularityCommand = append(singularityCommand, container.Image)
		singularityCommand = append(singularityCommand, pak.Commands...)
		singularityCommand = append(singularityCommand, pak.CommandArguments...)

		path, err := produceSlurmScript(pak, container, *prevJobID, singularityCommand)
		if err != nil {
			return errors.Wrap(err, "cannot generate slurm script")
		}

		out, err := slurmBatchSubmit(e, path)
		if err != nil {
			return errors.Wrapf(err, "Can't submit sbatch script")
		}

		// if sbatch script is successfully submitted check for job id
		jid, err := handleJobID(pak, container, out)
		if err != nil {
			return errors.Wrapf(err, "handleJobID")
		}

		*prevJobID = jid

		return WatchPath(ctx, pak.RuntimeDir())
	}
*/

/*
func produceSlurmScript(pak *api.KPod, container *corev1.Container, prevJobID int, command []string) (string, error) {
	sbatchRelativePath := filepath.Join(api.TemporaryDir, pak.Pod.GetName(), container.Name+".sh")

	var sbatchFlagsFromArgo []string
	var sbatchFlagsAsString = ""

	if err := os.MkdirAll(filepath.Join(api.TemporaryDir, pak.Pod.GetName()), api.SecretPodData); err != nil {
		return "", errors.Wrapf(err, "Could not create '%s' directory", filepath.Join(api.TemporaryDir, pak.Pod.GetName()))
	}

	if slurmFlags, ok := pak.Pod.GetAnnotations()["slurm-job.knoc.io/flags"]; ok {
		sbatchFlagsFromArgo = strings.Split(slurmFlags, " ")
	}

	for _, slurmFlag := range sbatchFlagsFromArgo {
		sbatchFlagsAsString += "\n#SBATCH " + slurmFlag
	}
	// SLURM_JOB_DEPENDENCY ensures sequential execution of init containers and main containers
	if prevJobID != -1 {
		sbatchFlagsAsString += "\n#SBATCH --dependency afterok:" + string(rune(prevJobID))
	}

	if mpiFlags, ok := pak.Pod.GetAnnotations()["slurm-job.knoc.io/mpi-flags"]; ok {
		if mpiFlags != "true" {
			mpi := append([]string{MPIEXEC, "-np", "$SLURM_NTASKS"}, strings.Split(mpiFlags, " ")...)
			command = append(mpi, command...)
		}
	}

	if err := writeSlurmScriptToFile(pak, sbatchFlagsAsString, container, command); err != nil {
		return "", errors.Wrap(err, "Can't produce sbatch script in file")
	}

	return sbatchRelativePath, nil
}

func slurmBatchSubmit(e *LocalExecutor, path string) (string, error) {
	var output []byte

	var err error

	output, err = exec.Command(e.sbatchExecutablePath, path).Output() //nolint:gosec
	if err != nil {
		return "", errors.Wrap(err, "Could not run sbatch. ")
	}

	return string(output), nil
}

func writeSlurmScriptToFile(pak *api.KPod, sbatchFlagsAsString string, c *corev1.Container, commandArray []string) error {

	ssfPath := pak.SBatchFilePath(c)

	scriptFile, err := os.Create(ssfPath)
	if err != nil {
		return errors.Wrapf(err, "Cant create slurm_script '%s'", ssfPath)
	}

	finalScriptData := sbatchMacros(pak.JobName(c), sbatchFlagsAsString) + "\n" +
		strings.Join(commandArray, " ") + " >> " + pak.StdOutputFilePath(c) +
		" 2>> " + pak.StdErrorFilePath(c) +
		"\n echo $? > " + pak.ExitCodeFilePath(c)

	if _, err = scriptFile.WriteString(finalScriptData); err != nil {
		return errors.Wrapf(err, "Can't write sbatch script in file '%s'", ssfPath)
	}

	if err := scriptFile.Close(); err != nil {
		return errors.Wrap(err, "Close")
	}

	return nil
}

func sbatchMacros(instanceName string, sbatchFlags string) string {
	return "#!/bin/bash" +
		"\n#SBATCH --job-name=" + instanceName +
		sbatchFlags +
		"\n. ~/.bash_profile" +
		"\npwd; hostname; date\n"
}

func handleJobID(pak *api.KPod, container *corev1.Container, output string) (int, error) {
	r := regexp.MustCompile(`Submitted batch job (?P<jid>\d+)`)
	jid := r.FindStringSubmatch(output)

	f, err := os.Create(pak.JobIDFilePath(container))
	f, err := os.Create(pak.JobIDFilePath(container))
	if err != nil {
		return -1, errors.Wrap(err, "Cant create jid_file")
	}

	if _, err := f.WriteString(jid[1]); err != nil {
		return -1, errors.Wrap(err, "Cant write jid_file")
	}
	intJobId, err := strconv.Atoi(jid[1])
	if err != nil {
		return 0, errors.Wrap(err, "Cant convert jid as integer. Parsed irregular output!")
	}
	return intJobId, f.Close()
}
*/
// TODO: restructure
// func DeleteContainer(c *corev1.Container) error {
//	data, err := os.ReadFile(".knoc/" + c.Name + ".jobID")
//	if err != nil {
//		return errors.Wrapf(err, "Can't find job id of container '%s'", c.Name)
//	}
//
//	jobID, err := strconv.Atoi(string(data))
//	if err != nil {
//		return errors.Wrapf(err, "Can't find job id of container '%s'", c.Name)
//	}
//
//	_, err = exec.Command(SCANCEL, fmt.Sprint(jobID)).Output()
//	if err != nil {
//		return errors.Wrapf(err, "Could not delete job '%s'", c.Name)
//	}
//
//	exec.Command("rm", "-f ", ".knoc/"+c.Name+".out")
//	exec.Command("rm", "-f ", ".knoc/"+c.Name+".err")
//	exec.Command("rm", "-f ", ".knoc/"+c.Name+".status")
//	exec.Command("rm", "-f ", ".knoc/"+c.Name+".jobID")
//	exec.Command("rm", "-rf", " .knoc/"+c.Name)
//
//	logrus.Info("Delete job", jobID)
//
//	return nil
// }

/*

	// in case we have initContainers we need to stop main containers from executing for now ...
	if len(pod.Spec.InitContainers) > 0 {
		state = waitingState
		hasInitContainers = true

		// run init container with remote execution enabled
		for i := range pod.Spec.InitContainers {
			// MUST TODO: Run init containers sequentialy and NOT all-together
			if err := RemoteExecution(p, ctx, api.SUBMIT, pod, &pod.Spec.InitContainers[i]); err != nil {
				return errors.Wrap(err, "remote execution failed")
			}
		}

		pod.Status = corev1.PodStatus{
			Phase:     corev1.PodRunning,
			HostIP:    "127.0.0.1",
			PodIP:     "127.0.0.1",
			StartTime: &now,
			Conditions: []corev1.PodCondition{
				{
					Type:   corev1.PodInitialized,
					Status: corev1.ConditionFalse,
				},
				{
					Type:   corev1.PodReady,
					Status: corev1.ConditionFalse,
				},
				{
					Type:   corev1.PodScheduled,
					Status: corev1.ConditionTrue,
				},
			},
		}
	} else {
		pod.Status = corev1.PodStatus{
			Phase:     corev1.PodRunning,
			HostIP:    "127.0.0.1",
			PodIP:     "127.0.0.1",
			StartTime: &now,
			Conditions: []corev1.PodCondition{
				{
					Type:   corev1.PodInitialized,
					Status: corev1.ConditionTrue,
				},
				{
					Type:   corev1.PodReady,
					Status: corev1.ConditionTrue,
				},
				{
					Type:   corev1.PodScheduled,
					Status: corev1.ConditionTrue,
				},
			},
		}
	}
	// deploy main containers
	for i, container := range pod.Spec.Containers {
		var err error

		if !hasInitContainers {
			if err := RemoteExecution(p, ctx, api.SUBMIT, pod, i); err != nil {
				return errors.Wrapf(err, "cannot execute container '%s'", container.Name)
			}
		}
		if err != nil {
			pod.Status.ContainerStatuses = append(pod.Status.ContainerStatuses, corev1.ContainerStatus{
				Name:         container.Name,
				Image:        container.Image,
				Ready:        false,
				RestartCount: 1,
				State: corev1.ContainerState{
					Terminated: &corev1.ContainerStateTerminated{
						Message:   "Could not reach remote cluster",
						StartedAt: now,
						ExitCode:  130,
					},
				},
			})
			pod.Status.Phase = corev1.PodFailed
			continue
		}
		pod.Status.ContainerStatuses = append(pod.Status.ContainerStatuses, corev1.ContainerStatus{
			Name:         container.Name,
			Image:        container.Image,
			Ready:        !hasInitContainers,
			RestartCount: 1,
			State:        state,
		})

	}
*/

/*

func checkPodsStatus(p *Provider, ctx context.Context) error {
	if len(p.pods) == 0 {
		return nil
	}

	log.GetLogger(ctx).Debug("received checkPodStatus")
	client, err := simplessh.ConnectWithKey(os.Getenv("REMOTE_HOST")+":"+os.Getenv("REMOTE_PORT"), os.Getenv("REMOTE_USER"), os.Getenv("REMOTE_KEY"))
	if err != nil {
		return errors.Wrapf(err, "cannot connnect")
	}

	defer client.Close()

	instanceName := ""
	now := metav1.Now()

	for _, pod := range p.pods {
		if pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed || pod.Status.Phase == corev1.PodPending {
			continue
		}

		// if it's not initialized yet
		if pod.Status.Conditions[0].Status == corev1.ConditionFalse && pod.Status.Conditions[0].Type == corev1.PodInitialized {
			containersCount := len(pod.Spec.InitContainers)
			successfull := 0
			failed := 0
			valid := 1

			for i, container := range pod.Spec.InitContainers {
				if len(pod.Status.InitContainerStatuses) < len(pod.Spec.InitContainers) {
					pod.Status.InitContainerStatuses = append(pod.Status.InitContainerStatuses, corev1.ContainerStatus{
						Name:         container.Name,
						Image:        container.Image,
						Ready:        true,
						RestartCount: 0,
						State: corev1.ContainerState{
							Running: &corev1.ContainerStateRunning{
								StartedAt: now,
							},
						},
					})
					continue
				}
				lastStatus := pod.Status.InitContainerStatuses[i]
				if lastStatus.Ready {

					statusFile, err := client.Exec("cat " + ".knoc/" + instanceName + ".status")
					status := string(statusFile)
					if len(status) > 1 {
						// remove '\n' from end of status due to golang's string conversion :X
						status = status[:len(status)-1]
					}

					if err != nil || status == "" {
						// still running
						continue
					}

					i, err := strconv.Atoi(status)
					reason := "Unknown"
					if i == 0 && err == nil {
						successfull++
						reason = "Completed"
					} else {
						failed++
						reason = "Error"
					}

					containersCount--
					pod.Status.InitContainerStatuses[i] = corev1.ContainerStatus{
						Name:  container.Name,
						Image: container.Image,
						Ready: false,
						State: corev1.ContainerState{
							Terminated: &corev1.ContainerStateTerminated{
								StartedAt:  lastStatus.State.Running.StartedAt,
								FinishedAt: now,
								Reason:     reason,
								ExitCode:   int32(i),
							},
						},
					}
					valid = 0
				} else {
					containersCount--
					status := lastStatus.State.Terminated.ExitCode
					i, _ := strconv.Atoi(string(status))
					if i == 0 {
						successfull++
					} else {
						failed++
					}
				}
			}
			if containersCount == 0 && pod.Status.Phase == corev1.PodRunning {
				if successfull == len(pod.Spec.InitContainers) {
					log.GetLogger(ctx).Debug("SUCCEEDED InitContainers")
					// PodInitialized = true
					pod.Status.Conditions[0].Status = corev1.ConditionTrue
					// PodReady = true
					pod.Status.Conditions[1].Status = corev1.ConditionTrue
					p.startMainContainers(ctx, pod)
					valid = 0
				} else {
					pod.Status.Phase = corev1.PodFailed
					valid = 0
				}
			}
			if valid == 0 {
				if err := p.UpdatePod(ctx, pod); err != nil {
					return errors.Wrapf(err, "update pod")
				}
			}
			// log.GetLogger(ctx).Infof("init checkPodStatus:%v %v %v", pod.Name, successfull, failed)
		} else {
			// if its initialized
			containersCount := len(pod.Spec.Containers)

			successfull := 0
			failed := 0
			valid := 1

			for i, container := range pod.Spec.Containers {

				lastStatus := pod.Status.ContainerStatuses[i]
				if lastStatus.Ready {
					statusFile, err := client.Exec("cat " + ".knoc/" + instanceName + ".status")
					status := string(statusFile)
					if len(status) > 1 {
						// remove '\n' from end of status due to golang's string conversion :X
						status = status[:len(status)-1]
					}
					if err != nil || status == "" {
						// still running
						continue
					}
					containersCount--
					i, err := strconv.Atoi(status)
					reason := "Unknown"
					if i == 0 && err == nil {
						successfull++
						reason = "Completed"
					} else {
						failed++
						reason = "Error"
						// log.GetLogger(ctx).Info("[checkPodStatus] CONTAINER_FAILED")
					}

					pod.Status.ContainerStatuses[i] = corev1.ContainerStatus{
						Name:  container.Name,
						Image: container.Image,
						Ready: false,
						State: corev1.ContainerState{
							Terminated: &corev1.ContainerStateTerminated{
								StartedAt:  lastStatus.State.Running.StartedAt,
								FinishedAt: now,
								Reason:     reason,
								ExitCode:   int32(i),
							},
						},
					}
					valid = 0
				} else {
					if lastStatus.State.Terminated == nil {
						// containers not yet turned on
						if p.activeInitContainers(pod) {
							continue
						}
					}
					containersCount--
					status := lastStatus.State.Terminated.ExitCode

					i := status
					if i == 0 && err == nil {
						successfull++
					} else {
						failed++
					}
				}
			}
			if containersCount == 0 && pod.Status.Phase == corev1.PodRunning {
				// containers are ready
				pod.Status.Conditions[1].Status = corev1.ConditionFalse

				if successfull == len(pod.Spec.Containers) {
					log.GetLogger(ctx).Debug("[checkPodStatus] POD_SUCCEEDED ")
					pod.Status.Phase = corev1.PodSucceeded
				} else {
					log.GetLogger(ctx).Debug("[checkPodStatus] POD_FAILED ", successfull, " ", containersCount, " ", len(pod.Spec.Containers), " ", failed)
					pod.Status.Phase = corev1.PodFailed
				}
				valid = 0
			}

			if valid == 0 {
				if err := p.UpdatePod(ctx, pod); err != nil {
					return errors.Wrapf(err, "update pod")
				}
			}

			log.GetLogger(ctx).Debugf("main checkPodStatus:%v %v %v", pod.Name, successfull, failed)
		}
	}

	return nil
}



	now := metav1.Now()
	pod.Status.Phase = corev1.PodSucceeded
	pod.Status.Reason = "KNOCProviderPodDeleted"


	for i := range pod.Status.ContainerStatuses {
		pod.Status.ContainerStatuses[i].Ready = false
		pod.Status.ContainerStatuses[i].State = corev1.ContainerState{
			Terminated: &corev1.ContainerStateTerminated{
				Message:    "KNOC provider terminated container upon deletion",
				FinishedAt: now,
				Reason:     "KNOCProviderPodContainerDeleted",
				// StartedAt:  pod.Status.ContainerStatuses[i].State.Running.StartedAt,
			},
		}
	}

	for idx := range pod.Status.InitContainerStatuses {
		pod.Status.InitContainerStatuses[idx].Ready = false
		pod.Status.InitContainerStatuses[idx].State = corev1.ContainerState{
			Terminated: &corev1.ContainerStateTerminated{
				Message:    "KNOC provider terminated container upon deletion",
				FinishedAt: now,
				Reason:     "KNOCProviderPodContainerDeleted",
				// StartedAt:  pod.Status.InitContainerStatuses[i].State.Running.StartedAt,
			},
		}
	}



func (p *Provider) activeInitContainers(pod *corev1.Pod) bool {
	activeInitContainers := len(pod.Spec.InitContainers)
	for idx, _ := range pod.Spec.InitContainers {
		if pod.Status.InitContainerStatuses[idx].State.Terminated != nil {
			activeInitContainers--
		}
	}
	return activeInitContainers != 0
}

func (p *Provider) startMainContainers(ctx context.Context, pod *corev1.Pod) {
	now := metav1.NewTime(time.Now())

	for i := range pod.Spec.Containers {
		container := &pod.Spec.Containers[i]

		err := RemoteExecution(p, ctx, api.SUBMIT, pod, container)
		if err != nil {
			pod.Status.ContainerStatuses[i] = corev1.ContainerStatus{
				Name:         container.Name,
				Image:        container.Image,
				Ready:        false,
				RestartCount: 1,
				State: corev1.ContainerState{
					Terminated: &corev1.ContainerStateTerminated{
						Message:   "Could not reach remote cluster",
						StartedAt: now,
						ExitCode:  130,
					},
				},
			}
			pod.Status.Phase = corev1.PodFailed
			continue
		}

		pod.Status.ContainerStatuses[i] = corev1.ContainerStatus{
			Name:         container.Name,
			Image:        container.Image,
			Ready:        true,
			RestartCount: 1,
			State: corev1.ContainerState{
				Running: &corev1.ContainerStateRunning{
					StartedAt: now,
				},
			},
		}

	}
}


	/*
		log.Println("A container stopped:", event.Name)
		pod, _, containerStatus := findPodAndContainerStatus(ctx, dir, file, inspect)

		_ = pod
		_ = containerStatus

*/

/*

	{
		log.Println("A container started by a slurm job:", event.Name)

		container, containerStatus, err := getContainer(ctx, dir, file, inspect)
		_ = err

		_ = pod
		_ = containerStatus

		// if pod.Status.Phase == corev1.PodSucceeded ||
		//	pod.Status.Phase == corev1.PodFailed ||
		//	pod.Status.Phase == corev1.PodPending {
		//	panic("should not be here")
		// }

		changeContainerStateToRunning(pod, container)
		// need to update pod status
		// need to add pod conditions

		if err := inspect.UpdatePod(ctx, pod); err != nil {
			panic(err)
		}
	}
*/

// check exitCode
// if exit ok
//	update container status to termination
// if last container of pod exited then update PodStatus

// if last initContainer exited then update PodCondition to initialized

// if any container exited with non-zero exit code update pod status to PodFailed

/*
// GetStatsSummary returns dummy stats for all pods known by this provider.
func (p *Provider) GetStatsSummary(ctx context.Context) (*stats.Summary, error) {
	var span trace.Span
	ctx, span = trace.StartSpan(ctx, "GetStatsSummary") // nolint: ineffassign,staticcheck
	defer span.End()

	// Grab the current timestamp so we can report it as the time the stats were generated.
	time := metav1.NewTime(time.Now())

	// Create the Summary object that will later be populated with node and pod stats.
	res := &stats.Summary{}

	// Populate the Summary object with basic node stats.
	res.Node = stats.NodeStats{
		NodeName:  p.nodeName,
		StartTime: metav1.NewTime(p.startTime),
	}

	// Populate the Summary object with dummy stats for each pod known by this provider.
	for _, pod := range p.pods {
		var (
			// totalUsageNanoCores will be populated with the sum of the values of UsageNanoCores computes across all containers in the pod.
			totalUsageNanoCores uint64
			// totalUsageBytes will be populated with the sum of the values of UsageBytes computed across all containers in the pod.
			totalUsageBytes uint64
		)

		// Create a PodStats object to populate with pod stats.
		pss := stats.PodStats{
			PodRef: stats.PodReference{
				Name:      pod.Name,
				Namespace: pod.Namespace,
				UID:       string(pod.UID),
			},
			StartTime: pod.CreationTimestamp,
		}

		// Iterate over all containers in the current pod to compute dummy stats.
		for _, container := range pod.Spec.Containers {
			// Grab a dummy value to be used as the total CPU usage.
			// The value should fit a uint32 in order to avoid overflows later on when computing pod stats.
			dummyUsageNanoCores := uint64(rand.Uint32())
			totalUsageNanoCores += dummyUsageNanoCores
			// Create a dummy value to be used as the total RAM usage.
			// The value should fit a uint32 in order to avoid overflows later on when computing pod stats.
			dummyUsageBytes := uint64(rand.Uint32())
			totalUsageBytes += dummyUsageBytes
			// Append a ContainerStats object containing the dummy stats to the PodStats object.
			pss.Containers = append(pss.Containers, stats.ContainerStats{
				Name:      container.Name,
				StartTime: pod.CreationTimestamp,
				CPU: &stats.CPUStats{
					Time:           time,
					UsageNanoCores: &dummyUsageNanoCores,
				},
				Memory: &stats.MemoryStats{
					Time:       time,
					UsageBytes: &dummyUsageBytes,
				},
			})
		}

		// Populate the CPU and RAM stats for the pod and append the PodsStats object to the Summary object to be returned.
		pss.CPU = &stats.CPUStats{
			Time:           time,
			UsageNanoCores: &totalUsageNanoCores,
		}
		pss.Memory = &stats.MemoryStats{
			Time:       time,
			UsageBytes: &totalUsageBytes,
		}
		res.Pods = append(res.Pods, pss)
	}

	// Return the dummy stats.
	return res, nil
}



/*
// if it's not initialized yet
		if pod.Status.Conditions[0].Status == corev1.ConditionFalse && pod.Status.Conditions[0].Type == corev1.PodInitialized {
			containersCount := len(pod.Spec.InitContainers)
			successfull := 0
			failed := 0
			valid := 1

			for i, container := range pod.Spec.InitContainers {
				if len(pod.Status.InitContainerStatuses) < len(pod.Spec.InitContainers) {
					pod.Status.InitContainerStatuses = append(pod.Status.InitContainerStatuses, corev1.ContainerStatus{
						Name:         container.Name,
						Image:        container.Image,
						Ready:        true,
						RestartCount: 0,
						State: corev1.ContainerState{
							Running: &corev1.ContainerStateRunning{
								StartedAt: now,
							},
						},
					})
					continue
				}
				lastStatus := pod.Status.InitContainerStatuses[i]
				if lastStatus.Ready {

					statusFile, err := client.Exec("cat " + ".knoc/" + instanceName + ".status")
					status := string(statusFile)
					if len(status) > 1 {
						// remove '\n' from end of status due to golang's string conversion :X
						status = status[:len(status)-1]
					}

					if err != nil || status == "" {
						// still running
						continue
					}

					i, err := strconv.Atoi(status)
					reason := "Unknown"
					if i == 0 && err == nil {
						successfull++
						reason = "Completed"
					} else {
						failed++
						reason = "Error"
					}

					containersCount--
					pod.Status.InitContainerStatuses[i] = corev1.ContainerStatus{
						Name:  container.Name,
						Image: container.Image,
						Ready: false,
						State: corev1.ContainerState{
							Terminated: &corev1.ContainerStateTerminated{
								StartedAt:  lastStatus.State.Running.StartedAt,
								FinishedAt: now,
								Reason:     reason,
								ExitCode:   int32(i),
							},
						},
					}
					valid = 0
				} else {
					containersCount--
					status := lastStatus.State.Terminated.ExitCode
					i, _ := strconv.Atoi(string(status))
					if i == 0 {
						successfull++
					} else {
						failed++
					}
				}
			}

			if containersCount == 0 && pod.Status.Phase == corev1.PodRunning {
				if successfull == len(pod.Spec.InitContainers) {
					log.GetLogger(ctx).Debug("SUCCEEDED InitContainers")
					// PodInitialized = true
					pod.Status.Conditions[0].Status = corev1.ConditionTrue
					// PodReady = true
					pod.Status.Conditions[1].Status = corev1.ConditionTrue
					p.startMainContainers(ctx, pod)
					valid = 0
				} else {
					pod.Status.Phase = corev1.PodFailed
					valid = 0
				}
			}
			if valid == 0 {
				if err := p.UpdatePod(ctx, pod); err != nil {
					return errors.Wrapf(err, "update pod")
				}
			}
			// log.GetLogger(ctx).Infof("init checkPodStatus:%v %v %v", pod.Name, successfull, failed)
		} else {
			// if its initialized
			containersCount := len(pod.Spec.Containers)

			successfull := 0
			failed := 0
			valid := 1

			for i, container := range pod.Spec.Containers {

				lastStatus := pod.Status.ContainerStatuses[i]
				if lastStatus.Ready {
					statusFile, err := client.Exec("cat " + ".knoc/" + instanceName + ".status")
					status := string(statusFile)
					if len(status) > 1 {
						// remove '\n' from end of status due to golang's string conversion :X
						status = status[:len(status)-1]
					}
					if err != nil || status == "" {
						// still running
						continue
					}
					containersCount--
					i, err := strconv.Atoi(status)
					reason := "Unknown"
					if i == 0 && err == nil {
						successfull++
						reason = "Completed"
					} else {
						failed++
						reason = "Error"
						// log.GetLogger(ctx).Info("[checkPodStatus] CONTAINER_FAILED")
					}

					pod.Status.ContainerStatuses[i] = corev1.ContainerStatus{
						Name:  container.Name,
						Image: container.Image,
						Ready: false,
						State: corev1.ContainerState{
							Terminated: &corev1.ContainerStateTerminated{
								StartedAt:  lastStatus.State.Running.StartedAt,
								FinishedAt: now,
								Reason:     reason,
								ExitCode:   int32(i),
							},
						},
					}
					valid = 0
				} else {
					if lastStatus.State.Terminated == nil {
						// containers not yet turned on
						if p.activeInitContainers(pod) {
							continue
						}
					}
					containersCount--
					status := lastStatus.State.Terminated.ExitCode

					i := status
					if i == 0 && err == nil {
						successfull++
					} else {
						failed++
					}
				}
			}
			if containersCount == 0 && pod.Status.Phase == corev1.PodRunning {
				// containers are ready
				pod.Status.Conditions[1].Status = corev1.ConditionFalse

				if successfull == len(pod.Spec.Containers) {
					log.GetLogger(ctx).Debug("[checkPodStatus] POD_SUCCEEDED ")
					pod.Status.Phase = corev1.PodSucceeded
				} else {
					log.GetLogger(ctx).Debug("[checkPodStatus] POD_FAILED ", successfull, " ", containersCount, " ", len(pod.Spec.Containers), " ", failed)
					pod.Status.Phase = corev1.PodFailed
				}
				valid = 0
			}

			if valid == 0 {
				if err := p.UpdatePod(ctx, pod); err != nil {
					return errors.Wrapf(err, "update pod")
				}
			}

			log.GetLogger(ctx).Debugf("main checkPodStatus:%v %v %v", pod.Name, successfull, failed)
		}
	}

	return nil
}
*/
