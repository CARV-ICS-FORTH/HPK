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
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/carv-ics-forth/hpk/api"
	"github.com/carv-ics-forth/hpk/pkg/path"
	"github.com/carv-ics-forth/hpk/pkg/process"
	"github.com/fsnotify/fsnotify"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

var DefaultLogger = zap.New(zap.UseDevMode(true))

/************************************************************

			Initiate Slurm Connector

************************************************************/

func init() {
	Slurm.SubmitCmd = path.GetPathOrDie("sbatch")
	Slurm.CancelCmd = path.GetPathOrDie("scancel")
	Slurm.SubmitTemplate = SBatchTemplate

	if err := StartReconciler(context.Background()); err != nil {
		panic(errors.Wrapf(err, "failed to start reconciler"))
	}
}

// Slurm represents a SLURM installation.
var Slurm struct {
	SubmitCmd string
	CancelCmd string

	// SubmitTemplate is the template to use for submissions to the HPC backend.
	SubmitTemplate string
}

func SubmitJob(scriptFile string) (string, error) {
	out, err := process.Execute(Slurm.SubmitCmd, scriptFile)
	if err != nil {
		return string(out), errors.Wrapf(err, "sbatch execution error")
	}

	return string(out), nil
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

var (
	DefaultMaxWorkers   = 1
	DefaultMaxQueueSize = 100

	fswatcher *fsnotify.Watcher
)

var fsEventHandler = FSEventDispatcher{
	Opts: Options{
		MaxWorkers:   DefaultMaxWorkers,
		MaxQueueSize: DefaultMaxQueueSize,
	},
	Queue:    make(chan fsnotify.Event, DefaultMaxQueueSize),
	locker:   sync.RWMutex{},
	Finished: false,
}

func StartReconciler(ctx context.Context) error {
	go fsEventHandler.Run(ctx)

	// Create new watcher.
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return errors.Wrapf(err, "add watcher on fsnotify failed")
	}

	fswatcher = watcher
	go func() {
		for {
			select {
			case event, ok := <-fswatcher.Events:
				if !ok {
					return
				}
				// add event to queue to be processed later
				fsEventHandler.Push(event)

			case err, ok := <-fswatcher.Errors:
				if !ok {
					return
				}

				panic(errors.Wrapf(err, "fsnotify error"))
				// TODO: I should handle better the errors from watchers
			}
		}
	}()

	return nil
}

var ErrClosedQueue = errors.New("queue is closed")

// Options represent options for FSEventDispatcher.
type Options struct {
	MaxWorkers   int // Number of workers to spawn.
	MaxQueueSize int // Maximum length for the queue to hold events.
}

// FSEventDispatcher represents the datastructure for an
// FSEventDispatcher instance. This struct satisfies the
// EventController interface.
type FSEventDispatcher struct {
	Opts  Options
	Queue chan fsnotify.Event

	locker   sync.RWMutex
	Finished bool
}

// Push adds a new event payload to the queue.
func (d *FSEventDispatcher) Push(event fsnotify.Event) {
	d.locker.RLock()
	defer d.locker.RUnlock()

	if d.Finished {
		DefaultLogger.Error(ErrClosedQueue, "event queue is closed")
		return
	}

	d.Queue <- event
}

// Run spawns workers and listens to the queue
// It's a blocking function and waits for a cancellation
// invocation from the Client.
func (d *FSEventDispatcher) Run(ctx context.Context) {
	waitGroup := sync.WaitGroup{}
	for i := 0; i < d.Opts.MaxWorkers; i++ {
		waitGroup.Add(1) // Add a wait group for each worker
		// Spawn a worker
		go func() {
			for {
				select {
				case <-ctx.Done():
					// Ensure no new messages are added.
					d.locker.Lock()
					d.Finished = true
					d.locker.Unlock()

					// Flush all events
					waitGroup.Done()

					return
				case event := <-d.Queue:
					if event.Op == fsnotify.Write {
						dir, file := filepath.Split(event.Name)

						fields := strings.Split(dir, "/")
						podKey := api.ObjectKey{
							Namespace: fields[1],
							Name:      fields[2],
						}

						logger := DefaultLogger.WithValues("pod", podKey)

						switch filepath.Ext(file) {
						case api.JobIdExtension: /* <---- Slurm Job Creation / Pod Scheduling */
							logger.Info("SLURM --> Event: Job Started", "file", file)

							if err := renewPodStatus(podKey); err != nil {
								logger.Error(err, "failed to handle job creation event")
							}

						case api.ExitCodeExtension: /*-- Slurm Job Completion / Pod Termination --*/
							logger.Info("SLURM --> Event: Job Completed", "file", file)

							if err := renewPodStatus(podKey); err != nil {
								logger.Error(err, "failed to handle job completion event")
							}

						default:
							logger.V(7).Info("Ignore fsnotify event", "op", "write", "file", file)
						}
					}
				}
			}
		}()
		waitGroup.Wait()
	}
}

func renewPodStatus(podKey api.ObjectKey) error {
	/*-- Load Pod from reference --*/
	pod, err := GetPod(podKey)
	if err != nil {
		return errors.Wrapf(err, "unable to load pod")
	}

	/*-- Recalculate the Pod status from locally stored containers --*/
	if err := podStateMapper(pod); err != nil {
		return errors.Wrapf(err, "unable to update pod status")
	}

	/*-- Update the top-level Pod description --*/
	if err := SavePod(pod); err != nil {
		return errors.Wrapf(err, "unable to store pod")
	}

	return nil
}

/************************************************************

			Set Shared Paths for Slurm runtime

************************************************************/

// RuntimeDir .hpk/namespace/podName.
func RuntimeDir(podRef api.ObjectKey) string {
	return filepath.Join(api.RuntimeDir, podRef.Namespace, podRef.Name)
}

// MountPaths .hpk/namespace/podName/mountName:mountPath
func MountPaths(podRef api.ObjectKey, mount corev1.VolumeMount) string {
	return filepath.Join(RuntimeDir(podRef), mount.Name+":"+mount.MountPath)
}

// SBatchDirectory .hpk/namespace/podName/.sbatch
func SBatchDirectory(podRef api.ObjectKey) string {
	return filepath.Join(RuntimeDir(podRef), ".sbatch")
}

// SBatchFilePath .hpk/namespace/podName/.sbatch/containerName.sh.
func SBatchFilePath(podRef api.ObjectKey, containerName string) string {
	return filepath.Join(SBatchDirectory(podRef), containerName+".sh")
}

// StdLogsPath .hpk/namespace/podName/containerName.{stdout,stderr}.
func StdLogsPath(podRef api.ObjectKey, containerName string) string {
	return filepath.Join(RuntimeDir(podRef), containerName)
}

// ExitCodeFilePath .hpk/namespace/podName/containerName.exitCode.
func ExitCodeFilePath(podRef api.ObjectKey, containerName string) string {
	return filepath.Join(RuntimeDir(podRef), containerName+".exitCode")
}

// JobIDFilePath .hpk/namespace/podName/containerName.jid.
func JobIDFilePath(podRef api.ObjectKey, containerName string) string {
	return filepath.Join(RuntimeDir(podRef), containerName+".jid")
}

// PodSpecFilePath .hpk/namespace/podName/podspec.json.
func PodSpecFilePath(podRef api.ObjectKey) string {
	return filepath.Join(RuntimeDir(podRef), "podspec.json")
}

func createSubDirectory(parent, name string) (string, error) {
	fullPath := filepath.Join(parent, name)

	if err := os.MkdirAll(fullPath, api.PodGlobalDirectoryPermissions); err != nil {
		return fullPath, errors.Wrapf(err, "cannot create dir '%s'", fullPath)
	}

	return fullPath, nil
}
