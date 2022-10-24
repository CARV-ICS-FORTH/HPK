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
	"bufio"
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/carv-ics-forth/hpk/api"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func renewPodStatus(ctx context.Context, podKey api.ObjectKey) error {
	/*-- Load Pod from reference --*/
	pod, err := GetPod(ctx, podKey)
	if err != nil {
		return errors.Wrapf(err, "unable to load pod")
	}

	/*-- Recalculate the Pod status from locally stored containers --*/
	if err := resolvePodStatus(pod); err != nil {
		return errors.Wrapf(err, "unable to update pod status")
	}

	/*-- Update the top-level Pod description --*/
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
	 * Filter-out Pods with Unsupported Fields
	 *---------------------------------------------------*/
	if pod.GetNamespace() == "kube-system" {
		pod.Status.Phase = corev1.PodFailed
		pod.Status.Reason = "UnsupportedFeatures"
		pod.Status.Message = "This version of HPK is not intended for kube-system jobs"

		return nil
	}

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
	logger.Info(" O Checking for status of Containers")

	var state Classifier
	state.Reset()

	for i, containerStatus := range pod.Status.ContainerStatuses {
		state.Classify(containerStatus.Name, &pod.Status.ContainerStatuses[i])
	}

	totalJobs := len(pod.Spec.Containers)

	/*---------------------------------------------------
	 * Define Expected Lifecycle Transitions
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

	/*---------------------------------------------------
	 * Check for Expected Lifecycle Transitions or panic
	 *---------------------------------------------------*/
	for _, testcase := range testSequence {
		if testcase.expression {
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
	 * Generic Handler for ContainerStatus
	 *---------------------------------------------------*/
	handleStatus := func(containerStatus *corev1.ContainerStatus) {
		if containerStatus.RestartCount > 0 {
			panic("Restart is not yet supported")
		}

		jobIDPath := JobIDFilePath(podKey, containerStatus.Name)
		jobID, jobIDExists := readStringFromFile(jobIDPath)

		/*-- StateWaiting: no jobid, means that the job is still in the Slurm queue --*/
		if !jobIDExists {
			containerStatus.State.Waiting = &corev1.ContainerStateWaiting{
				Reason:  "JobWaitingInSlurm",
				Message: "Job waiting in the Slurm queue",
			}
		}

		/*-- StateRunning: existing jobid, means that the job is running --*/
		if jobIDExists && containerStatus.State.Running == nil {
			/*-- fields driven by probes. --*/
			started := true
			containerStatus.Started = &started
			containerStatus.Ready = true

			containerStatus.State.Running = &corev1.ContainerStateRunning{
				StartedAt: metav1.Now(), // fixme: we should get this info from the file's ctime
			}
		}

		/*-- StateTerminated: existing exitcode, means that the job is complete --*/
		exitCodePath := ExitCodeFilePath(podKey, containerStatus.Name)
		exitCode, exitCodeExists := readIntFromFile(exitCodePath)

		if exitCodeExists {
			var reason string

			if exitCode == 0 {
				reason = "Success"
			} else {
				reason = "Failed"
			}

			containerStatus.State.Terminated = &corev1.ContainerStateTerminated{
				ExitCode:    int32(exitCode),
				Signal:      0,
				Reason:      reason,
				Message:     trytoDecodeExitCode(exitCode),
				StartedAt:   containerStatus.State.Running.StartedAt,
				FinishedAt:  metav1.Now(), // fixme: get it from the file's ctime
				ContainerID: jobID,
			}

			containerStatus.LastTerminationState = containerStatus.State
		}
	}

	/*---------------------------------------------------
	 * Iterate containers and call the Generic Handler
	 *---------------------------------------------------*/
	for i := 0; i < len(pod.Status.InitContainerStatuses); i++ {
		handleStatus(&pod.Status.InitContainerStatuses[i])
	}

	for i := 0; i < len(pod.Status.ContainerStatuses); i++ {
		handleStatus(&pod.Status.ContainerStatuses[i])
	}

	return nil
}

// awesome work: https://komodor.com/learn/exit-codes-in-containers-and-kubernetes-the-complete-guide/
func trytoDecodeExitCode(code int) string {
	switch code {
	case 0:
		return "Purposely Stopped"
	case 1:
		return "Application error"
	case 125:
		return "Container failed to run error"
	case 126:
		return "Command invoke error"
	case 127:
		return "File or directory not found"
	case 128:
		return "Invalid argument used on exit"
	case 134:
		return "Abnormal termination (SIGABRT)"
	case 137:
		return "Immediate termination (SIGKILL)"
	case 139:
		return "Segmentation fault (SIGSEGV)"
	case 143:
		return "Graceful termination (SIGTERM)"
	case 255:
		return "Exit Status Out Of Range"
	default:
		return "Unknown Exist Code"
	}
}

func readStringFromFile(filepath string) (string, bool) {
	out, err := os.ReadFile(filepath)
	if os.IsNotExist(err) {
		return "", false
	}

	if err != nil {
		defaultLogger.Error(err, "cannot read file", "path", filepath)
		return "", false
	}

	return string(out), true
}

func readIntFromFile(filepath string) (int, bool) {
	out, err := os.ReadFile(filepath)
	if os.IsNotExist(err) {
		return -1, false
	}

	if err != nil {
		defaultLogger.Error(err, "cannot read file", "path", filepath)
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
