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

// Package slurm contains code for accessing compute resources via Slurm.
package slurm

import (
	"context"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/carv-ics-forth/hpk/compute"
	"github.com/carv-ics-forth/hpk/pkg/process"
	"github.com/fsnotify/fsnotify"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

/************************************************************

			Initiate Slurm Connector

************************************************************/

// ExcludeNodes EXISTS ONLY FOR DEBUGGING PURPOSES of Inotify on NFS.
var ExcludeNodes = "--exclude="

func init() {
	Slurm.SubmitCmd = "sbatch"  // path.GetPathOrDie("sbatch")
	Slurm.CancelCmd = "scancel" // path.GetPathOrDie("scancel")
	Slurm.StatsCmd = "sinfo"
}

// Slurm represents a SLURM installation.
var Slurm struct {
	SubmitCmd string
	CancelCmd string
	StatsCmd  string
}

// ConnectionOK return true if HPK maintains connection with the Slurm manager.
// Otherwise, it returns false.
func ConnectionOK() bool {
	return true
}

func SubmitJob(scriptFile string) (string, error) {
	out, err := process.ExecuteInDir(compute.UserHomeDir, Slurm.SubmitCmd, ExcludeNodes, scriptFile)
	if err != nil {
		SystemError(err, "sbatch submission error. out : '%s'", out)
	}

	expectedOutput := regexp.MustCompile(`Submitted batch job (?P<jid>\d+)`)
	jid := expectedOutput.FindStringSubmatch(string(out))

	if _, err := strconv.Atoi(jid[1]); err != nil {
		SystemError(err, "Invalid JobID")
	}

	return jid[1], nil
}

func CancelJob(args string) (string, error) {
	out, err := process.Execute(Slurm.CancelCmd, args)
	if err != nil {
		return string(out), errors.Wrap(err, "Could not run scancel")
	}

	return string(out), nil
}

/************************************************************

			Listen for SLURM events

************************************************************/

var ErrClosedQueue = errors.New("queue is closed")

// Options represent options for SlurmEventHandler.
type Options struct {
	MaxWorkers   int // Number of workers to spawn.
	MaxQueueSize int // Maximum length for the queue to hold events.
}

func NewEventHandler(opts Options) *EventHandler {
	return &EventHandler{
		Opts:     opts,
		Queue:    make(chan fsnotify.Event, opts.MaxQueueSize),
		locker:   sync.RWMutex{},
		Finished: false,
	}
}

// EventHandler represents the datastructure for an
// EventHandler instance. This struct satisfies the
// EventHandler interface.
type EventHandler struct {
	Opts  Options
	Queue chan fsnotify.Event

	locker   sync.RWMutex
	Finished bool
}

// Push adds a new event payload to the queue.
func (h *EventHandler) Push(event fsnotify.Event) {
	h.locker.RLock()
	defer h.locker.RUnlock()

	if h.Finished {
		compute.DefaultLogger.Error(ErrClosedQueue, "event queue is closed")
		return
	}

	h.Queue <- event
}

// Run spawns workers and listens to the queue
// It's a blocking function and waits for a cancellation
// invocation from the Client.
func (h *EventHandler) Run(ctx context.Context, notifyVirtualKubelet func(pod *corev1.Pod)) {
	waitGroup := sync.WaitGroup{}
	for i := 0; i < h.Opts.MaxWorkers; i++ {
		waitGroup.Add(1)

		go func() {
			for {
				select {
				case <-ctx.Done():
					compute.DefaultLogger.Info("Shutting down the Slurm listener", "err", ctx.Err())

					// Ensure no new messages are added.
					h.locker.Lock()
					h.Finished = true
					h.locker.Unlock()

					// Mark worker as done
					waitGroup.Done()

					return
				case event := <-h.Queue:
					podkey, file, invalid := compute.ParsePath(event.Name)
					if invalid {
						compute.DefaultLogger.Info("SLURM: omit unexpected event", "op", event.Op, "event", event.Name)
						continue
					}

					logger := compute.DefaultLogger.WithValues("pod", podkey)

					/*-- Skip events that are not related to pod changes driven by Slurm --*/
					/* In previous versions, this condition was fsnotify.Write, with the goal to avoid race
					conditions between creating a file and writing a file. However, this does not seem to work
					with the Polling watcher, and we can only capture Create events. In turn, that means that
					file readers must retry if there are no contents in the file.
					*/
					if !(event.Op.Has(fsnotify.Create) || event.Op.Has(fsnotify.Write)) {
						logger.Info("SLURM: omit known event", "op", event.Op, "file", file)
						continue
					}

					/*---------------------------------------------------
					 * Declare events that warrant Pod reconciliation
					 *---------------------------------------------------*/
					ext := filepath.Ext(file)
					switch ext {
					case compute.ExtensionSysError:
						/*-- Sbatch failed. Pod should fail immediately without other checks --*/
						logger.Info("SLURM -> Pod initialization error", "op", event.Op, "file", file)

						pod := LoadPod(podkey)
						if pod == nil {
							SystemError(errors.Errorf("pod '%s' does not exist", podkey), "ERR")
						}

						PodError(pod, "SYSERROR", "Here should go the content of the file")

						/*-- Update the remote Copy --*/
						notifyVirtualKubelet(pod)

						logger.Info("** K8s Status Updated **",
							"version", pod.ResourceVersion,
							"phase", pod.Status.Phase,
						)

						continue

					case compute.ExtensionIP:
						/*-- Sbatch Started --*/
						logger.Info("SLURM: New Pod IP", "op", event.Op, "file", file)

					case compute.ExtensionJobID:
						/*-- Container Started --*/
						logger.Info("SLURM: Container Started", "op", event.Op, "file", file)

						// FIXME: create k8s event "Started container CONTAINERNAME"

					case compute.ExtensionExitCode:
						/*-- Container Terminated --*/
						logger.Info("SLURM: Container Terminated", "op", event.Op, "file", file)

					default:
						/*-- Any other file gnored --*/
						// logger.Info("Ignore fsnotify event", "op", event.Op, "file", file)

						continue
					}

					/*---------------------------------------------------
					 * Reconcile Pod and Notify Virtual Kubelet
					 *---------------------------------------------------*/

					/*-- Load Pod from reference --*/
					pod := LoadPod(podkey)
					if pod == nil {
						// Race conditions may between the deletion of a pod and Slurm events.
						logger.Info("pod was not found. this is probably a conflict. omit event")
						continue
					}

					/*-- Recalculate the Pod status from locally stored containers --*/
					podStateMapper(pod)

					/*-- Update the remote Copy --*/
					notifyVirtualKubelet(pod)

					logger.Info("** K8s Status Updated **",
						"version", pod.ResourceVersion,
						"phase", pod.Status.Phase,
					)
				}
			}
		}()

		waitGroup.Wait()
	}
}

/************************************************************

		Known Job Types

************************************************************/

type JobIDType string

const (
	JobIDTypeInstance JobIDType = "instance://"

	JobIDTypeProcess JobIDType = "pid://"

	JobIDTypeSlurm JobIDType = "slurm://"

	JobIDTypeEmpty JobIDType = ""
)

func SetPodID(pod *corev1.Pod, idType JobIDType, value string) {
	metav1.SetMetaDataAnnotation(&pod.ObjectMeta, "pod.hpk/id", string(idType)+value)
}

func SetContainerStatusID(status *corev1.ContainerStatus, typedValue string) {
	// ensure that the value follows an expected format.
	_, _ = parseIDType(typedValue)

	status.ContainerID = typedValue
}

func ParsePodID(pod *corev1.Pod) (idType JobIDType, value string) {
	raw, exists := pod.GetAnnotations()["pod.hpk/id"]

	if !exists {
		return JobIDTypeEmpty, ""
	}

	return parseIDType(raw)
}

func ParseContainerID(status *corev1.ContainerStatus) (idType JobIDType, value string) {
	return parseIDType(status.ContainerID)
}

func parseIDType(raw string) (idType JobIDType, value string) {
	/*-- Extract id from raw format '<type>://<job_id>'. --*/
	switch {
	case strings.HasPrefix(raw, string(JobIDTypeSlurm)):
		return JobIDTypeSlurm, strings.Split(raw, string(JobIDTypeSlurm))[1]
	case strings.HasPrefix(raw, string(JobIDTypeInstance)):
		return JobIDTypeInstance, strings.Split(raw, string(JobIDTypeInstance))[1]
	case strings.HasPrefix(raw, string(JobIDTypeProcess)):
		return JobIDTypeProcess, strings.Split(raw, string(JobIDTypeProcess))[1]
	default:
		panic("unknown id format: " + raw)
	}
}
