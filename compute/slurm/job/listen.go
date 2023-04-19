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

// Package job contains code for accessing compute resources via Slurm.
package job

import (
	"context"
	"path/filepath"
	"sync"

	"github.com/carv-ics-forth/hpk/compute"
	"github.com/fsnotify/fsnotify"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

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

type PodControl struct {
	SyncStatus           func(pod *corev1.Pod)
	LoadFromDisk         func(podRef client.ObjectKey) *corev1.Pod
	NotifyVirtualKubelet func(pod *corev1.Pod)
}

// Run spawns workers and listens to the queue
// It's a blocking function and waits for a cancellation
// invocation from the Client.
func (h *EventHandler) Run(ctx context.Context, control PodControl) {
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

						pod := control.LoadFromDisk(podkey)
						if pod == nil {
							compute.SystemError(errors.Errorf("pod '%s' does not exist", podkey), "ERR")
						}

						compute.PodError(pod, "SYSERROR", "Here should go the content of the file")

						/*-- Update the remote Copy --*/
						control.NotifyVirtualKubelet(pod)

						logger.Info("** K8s Status Updated (SYSERR) **",
							"version", pod.ResourceVersion,
							"phase", pod.Status.Phase,
						)

						continue

					case compute.ExtensionIP:
						/*-- Sbatch Started --*/
						logger.Info("SLURM Event: New Pod IP", "op", event.Op, "file", file)

					case compute.ExtensionJobID:
						/*-- Container Started --*/
						logger.Info("SLURM Event: Container Started", "op", event.Op, "file", file)

					case compute.ExtensionExitCode:
						/*-- Container Terminated --*/
						logger.Info("SLURM Event: Container Terminated", "op", event.Op, "file", file)

					default:
						/*-- Any other file gnored --*/
						// logger.Info("Ignore fsnotify event", "op", event.Op, "file", file)

						continue
					}

					/*---------------------------------------------------
					 * Reconcile Pod and Notify Virtual Kubelet
					 *---------------------------------------------------*/

					/*-- Load Pod from reference --*/
					pod := control.LoadFromDisk(podkey)
					if pod == nil {
						// Race conditions may between the deletion of a pod and Slurm events.
						logger.Info("pod was not found. this is probably a conflict. omit event")
						continue
					}

					/*-- Recalculate the Pod status from locally stored containers --*/
					control.SyncStatus(pod)

					/*-- Update the remote Copy --*/
					control.NotifyVirtualKubelet(pod)

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
