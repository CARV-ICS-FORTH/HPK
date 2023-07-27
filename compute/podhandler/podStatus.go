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
	"bufio"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/carv-ics-forth/hpk/compute"
	"github.com/carv-ics-forth/hpk/pkg/crdtools"
	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func SyncPodStatus(pod *corev1.Pod) {
	podKey := client.ObjectKeyFromObject(pod)
	podDir := compute.HPK.Pod(podKey)
	logger := compute.DefaultLogger.WithValues("pod", podKey)

	/*---------------------------------------------------
	 * Handle Initialization and Finals States
	 *---------------------------------------------------*/
	switch pod.Status.Phase {
	case "":
		/*-- If met for first time, check for unsupported fields --*/
		if podWithExplicitlyUnsupportedFields(logger, pod) {
			return
		}
	case corev1.PodSucceeded, corev1.PodFailed:
		/*-- If on final states, there is nothing else to do --*/
		return
	}

	/*-- Initialization of virtual environment (e.g, sbatch code, IP, ...)  --*/
	if pod.Status.PodIP == "" {
		podIPPath := podDir.IPAddressPath()
		ip, ok := readStringFromFile(podIPPath)
		if ok {
			pod.Status.PodIP = ip
			pod.Status.PodIPs = append(pod.Status.PodIPs, corev1.PodIP{IP: ip})
		}
	}

	/*---------------------------------------------------
	 * Load Container Statuses
	 *---------------------------------------------------*/
	SyncContainerStatuses(pod)

	/*---------------------------------------------------
	 * Check status of Init Containers
	 *---------------------------------------------------*/
	// https://kubernetes.io/docs/concepts/workloads/pods/init-containers/

	/*-- A Pod that is initializing is in the Pending state --*/
	if pod.Status.Phase == corev1.PodPending {
		for _, initContainer := range pod.Status.InitContainerStatuses {
			if initContainer.State.Terminated == nil {
				/*-- Still Initializing: at least one init container is still running --*/
				return
			} else {
				/*-- Initialization error: at least one init container has failed --*/
				if initContainer.State.Terminated.ExitCode != 0 {
					compute.PodError(pod, compute.ReasonInitializationError, "Init container '%s' has failed", initContainer.Name)

					return
				}
			}
		}

		/*-- Pod is Ready: all init containers have completed successfully --*/
		crdtools.SetPodStatusCondition(&pod.Status.Conditions, corev1.PodCondition{
			Type:   corev1.PodInitialized,
			Status: corev1.ConditionTrue,
			// LastProbeTime:      metav1.Time{},
			LastTransitionTime: metav1.Now(),
			Reason:             "Initialized",
			Message:            "all init containers in the pod have started successfully",
		})

		/*--
			Set the transition between completion of init containers and starting of normal containers.
			This transition may take arbitrary time, if for example we need to pull the container images.
		--*/
		pod.Status.Message = "Waiting"
		pod.Status.Reason = "ContainerCreating"
	}

	/*---------------------------------------------------
	 * Classify container statuses
	 *---------------------------------------------------*/
	var state Classifier
	state.Reset()

	for i, containerStatus := range pod.Status.ContainerStatuses {
		state.Classify(containerStatus.Name, &pod.Status.ContainerStatuses[i])
	}

	totalJobs := len(pod.Spec.Containers)

	/*---------------------------------------------------
	 * Define Expected Lifecycle Transitions
	 *---------------------------------------------------*/
	type transition struct {
		expression bool
		change     func(status *corev1.PodStatus)
	}

	phaseTransitionSequence := []transition{
		{ /*-- FAILED: at least one job has failed --*/
			expression: state.NumFailedJobs() > 0,
			change: func(status *corev1.PodStatus) {
				status.Phase = corev1.PodFailed
				status.Reason = "ContainerFailed"
				status.Message = fmt.Sprintf("Failed containers: %s", state.ListFailedJobs())

				setTerminationConditions(pod)
			},
		},

		{ /*-- SUCCESS: all jobs are successfully completed --*/
			expression: state.NumSuccessfulJobs() == totalJobs,
			change: func(status *corev1.PodStatus) {
				status.Phase = corev1.PodSucceeded
				status.Reason = "Success"
				status.Message = fmt.Sprintf("Success containers: %s", state.ListSuccessfulJobs())

				setTerminationConditions(pod)
			},
		},

		{ /*-- RUNNING: one job is still running --*/
			expression: state.NumRunningJobs()+state.NumSuccessfulJobs() == totalJobs,
			change: func(status *corev1.PodStatus) {
				status.Phase = corev1.PodRunning
				status.Reason = "Running"
				status.Message = "at least one pod is still running"

				/*-- ContainersReady: all containers in the pod are ready. --*/
				crdtools.SetPodStatusCondition(&pod.Status.Conditions, corev1.PodCondition{
					Type:   corev1.ContainersReady,
					Status: corev1.ConditionTrue,
					// LastProbeTime:      metav1.Time{},
					LastTransitionTime: metav1.Now(),
					Reason:             "ContainersReady",
					Message:            " all containers in the pod are ready.",
				})

				/*-- PodReady: the pod is able to service requests and should be added to the
				  load balancing pools of all matching services. --*/
				crdtools.SetPodStatusCondition(&pod.Status.Conditions, corev1.PodCondition{
					Type:   corev1.PodReady,
					Status: corev1.ConditionTrue,
					// LastProbeTime:      metav1.Time{},
					LastTransitionTime: metav1.Now(),
					Reason:             "PodReady",
					Message:            "the pod is able to service requests",
				})
			},
		},

		{ /*-- PENDING: some jobs are not yet created --*/
			expression: state.NumPendingJobs() > 0,
			change: func(status *corev1.PodStatus) {
				status.Phase = corev1.PodPending
				status.Reason = "InQueue"
				status.Message = fmt.Sprintf("PendingJobs: %s", state.ListPendingJobs())
			},
		},

		{ /*-- FAILED: invalid state transition --*/
			expression: true,
			change: func(status *corev1.PodStatus) {
				status.Phase = corev1.PodFailed
				status.Reason = "PodFailure"
				status.Message = "Add some explanatory message here"
			},
		},
	}

	/*---------------------------------------------------
	 * Check for Expected Lifecycle Transitions or panic
	 *---------------------------------------------------*/
	for _, testcase := range phaseTransitionSequence {
		if testcase.expression {
			testcase.change(&pod.Status)

			logger.Info(" * Local Pod status has changed",
				"phase", pod.Status.Phase,
				"completed", state.ListSuccessfulJobs(),
				"failed", state.ListFailedJobs(),
			)

			return
		}
	}

	panic(errors.Errorf(`unhandled lifecycle conditions.
			current: '%v',
			totalJobs: '%d',
			jobs: '%s',
		 `, pod.Status.Phase, totalJobs, state.ListAll()))
}

func setTerminationConditions(pod *corev1.Pod) {
	crdtools.SetPodStatusCondition(&pod.Status.Conditions, corev1.PodCondition{
		Type:   corev1.ContainersReady,
		Status: corev1.ConditionFalse,
		// LastProbeTime:      metav1.Time{},
		LastTransitionTime: metav1.Now(),
		Reason:             "ContainersUnready",
		Message:            "Pod Has been Successfully Terminated.",
	})

	crdtools.SetPodStatusCondition(&pod.Status.Conditions, corev1.PodCondition{
		Type:   corev1.PodReady,
		Status: corev1.ConditionFalse,
		// LastProbeTime:      metav1.Time{},
		LastTransitionTime: metav1.Now(),
		Reason:             "PodUnready",
		Message:            "Pod Has been Successfully Terminated.",
	})
}

func podWithExplicitlyUnsupportedFields(logger logr.Logger, pod *corev1.Pod) bool {
	var unsupportedFields []string

	/*---------------------------------------------------
	 * Unsupported Pod-Level Fields
	 *---------------------------------------------------*/
	if pod.GetNamespace() == "kube-system" {
		unsupportedFields = append(unsupportedFields, ".Meta.Namespace == 'kube-system'")
	}

	if pod.Spec.Affinity != nil {
		logger.Info("Ignore .Spec.Affinity")
		// unsupportedFields = append(unsupportedFields, ".Spec.Affinity")
	}

	if pod.Spec.DNSConfig != nil {
		unsupportedFields = append(unsupportedFields, ".Spec.DNSConfig")
	}

	if pod.Spec.SecurityContext != nil {
		logger.Info("Ignore .Spec.SecurityContext")
		//	unsupportedFields = append(unsupportedFields, ".Spec.SecurityContext")
	}

	/*---------------------------------------------------
	 * Unsupported Container-Level Fields
	 *---------------------------------------------------*/
	for i, container := range pod.Spec.Containers {
		if container.SecurityContext != nil {
			logger.Info(fmt.Sprintf("Ignore .Spec.Containers[%d].SecurityContext", i))
			// unsupportedFields = append(unsupportedFields, fmt.Sprintf(".Spec.Containers[%d].SecurityContext", i))
		}

		if container.StartupProbe != nil {
			logger.Info(fmt.Sprintf("Ignore .Spec.Containers[%d].StartupProbe", i))
			// unsupportedFields = append(unsupportedFields, fmt.Sprintf(".Spec.Containers[%d].StartupProbe", i))
		}

		if container.LivenessProbe != nil {
			logger.Info(fmt.Sprintf("Ignore .Spec.Containers[%d].LivenessProbe", i))
			// unsupportedFields = append(unsupportedFields, fmt.Sprintf(".Spec.Containers[%d].LivenessProbe", i))
		}

		if container.ReadinessProbe != nil {
			logger.Info(fmt.Sprintf("Ignore .Spec.Containers[%d].ReadinessProbe", i))
			// unsupportedFields = append(unsupportedFields, fmt.Sprintf(".Spec.Containers[%d].ReadinessProbe", i))
		}
	}

	/*---------------------------------------------------
	 * Summary of Unsupported Fields
	 *---------------------------------------------------*/
	if len(unsupportedFields) > 0 {
		compute.PodError(pod, compute.ReasonUnsupportedFeatures, "UnsupportedFeatures: %s", strings.Join(unsupportedFields, ","))

		return true
	}

	return false
}

// HumanReadableCode translated the exit into a human-readable form.
// Source: https://komodor.com/learn/exit-codes-in-containers-and-kubernetes-the-complete-guide/
func HumanReadableCode(code int) string {
	switch code {
	case 0:
		return "Container exited"
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
		return "Unknown Exit Code"
	}
}

func readStringFromFile(filepath string) (string, bool) {
	out, err := os.ReadFile(filepath)
	if os.IsNotExist(err) {
		return "", false
	}

	if err != nil {
		compute.DefaultLogger.Error(err, "cannot read file", "path", filepath)
		return "", false
	}

	// filter any new line on file
	return strings.TrimSuffix(string(out), "\n"), true
}

func readIntFromFile(filepath string) (int, bool) {
	out, err := os.ReadFile(filepath)
	if os.IsNotExist(err) {
		return -1, false
	}

	if err != nil {
		compute.DefaultLogger.Error(err, "cannot read file", "path", filepath)
		return -1, false
	}

	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	scanner.Split(bufio.ScanWords)

	if scanner.Scan() {
		code, err := strconv.Atoi(scanner.Text())
		if err != nil {
			compute.SystemPanic(err, "cannot decode content to int")
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
