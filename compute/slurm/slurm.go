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

	"github.com/carv-ics-forth/hpk/pkg/process"
	"github.com/fsnotify/fsnotify"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

var DefaultLogger = zap.New(zap.UseDevMode(true))

/************************************************************

			Set Shared Paths for Slurm runtime

************************************************************/

/*
+-----+---+--------------------------+
| rwx | 7 | Read, write and execute  |
| rw- | 6 | Read, write              |
| r-x | 5 | Read, and execute        |
| r-- | 4 | Read,                    |
| -wx | 3 | Write and execute        |
| -w- | 2 | Write                    |
| --x | 1 | Execute                  |
| --- | 0 | no permissions           |
+------------------------------------+

+------------+------+-------+
| Permission | Octal| Field |
+------------+------+-------+
| rwx------  | 0700 | User  |
| ---rwx---  | 0070 | Group |
| ------rwx  | 0007 | Other |
+------------+------+-------+
*/

const (
	PodGlobalDirectoryPermissions = 0o777
	PodSpecJsonFilePermissions    = 0o600
	ContainerJobPermissions       = 0o777
)

const (
	RuntimeDir = ".hpk"

	PodIPExtension   = ".ip"
	PodJSONExtension = ".crd"

	ContainerExitCodeExtension = ".exitCode"
	ContainerJobIdExtension    = ".jid"
)

// PodRuntimeDir .hpk/namespace/podName.
func PodRuntimeDir(podRef client.ObjectKey) string {
	return filepath.Join(RuntimeDir, podRef.Namespace, podRef.Name)
}

// PodIPFilepath .hpk/namespace/podName/pod.ip
func PodIPFilepath(podRef client.ObjectKey) string {
	return filepath.Join(PodRuntimeDir(podRef), PodIPExtension)
}

// PodJSONFilepath .hpk/namespace/podName/podspec.json.
func PodJSONFilepath(podRef client.ObjectKey) string {
	return filepath.Join(PodRuntimeDir(podRef), PodJSONExtension)
}

// PodMountpaths .hpk/namespace/podName/mountName:mountPath
func PodMountpaths(podRef client.ObjectKey, mount corev1.VolumeMount) string {
	return filepath.Join(PodRuntimeDir(podRef), mount.Name+":"+mount.MountPath)
}

// PodScriptsDir .hpk/namespace/podName/.sbatch
func PodScriptsDir(podRef client.ObjectKey) string {
	return filepath.Join(PodRuntimeDir(podRef), ".scripts")
}

// ContainerStdoutFilepath .hpk/namespace/podName/containerName.{stdout,stderr}.
func ContainerStdoutFilepath(podRef client.ObjectKey, containerName string) string {
	return filepath.Join(PodRuntimeDir(podRef), containerName+".stdout")
}

// ContainerStderrFilepath .hpk/namespace/podName/containerName.{stdout,stderr}.
func ContainerStderrFilepath(podRef client.ObjectKey, containerName string) string {
	return filepath.Join(PodRuntimeDir(podRef), containerName+".stderr")
}

// ContainerJobIDFilepath .hpk/namespace/podName/containerName.jid.
func ContainerJobIDFilepath(podRef client.ObjectKey, containerName string) string {
	return filepath.Join(PodRuntimeDir(podRef), containerName+ContainerJobIdExtension)
}

// ContainerExitCodeFilepath .hpk/namespace/podName/containerName.exitCode.
func ContainerExitCodeFilepath(podRef client.ObjectKey, containerName string) string {
	return filepath.Join(PodRuntimeDir(podRef), containerName+ContainerExitCodeExtension)
}

func createSubDirectory(parent, name string) (string, error) {
	fullPath := filepath.Join(parent, name)

	if err := os.MkdirAll(fullPath, PodGlobalDirectoryPermissions); err != nil {
		return fullPath, errors.Wrapf(err, "cannot create dir '%s'", fullPath)
	}

	return fullPath, nil
}

/************************************************************

			Initiate Slurm Connector

************************************************************/

func init() {
	Slurm.SubmitCmd = "sbatch"  // path.GetPathOrDie("sbatch")
	Slurm.CancelCmd = "scancel" // path.GetPathOrDie("scancel")
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

						podKey := client.ObjectKey{
							Namespace: fields[1],
							Name:      fields[2],
						}

						logger := DefaultLogger.WithValues("pod", podKey)

						/*---------------------------------------------------
						 * Declare events that warrant Pod reconciliation
						 *---------------------------------------------------*/
						switch filepath.Ext(file) {
						case PodIPExtension:
							/*-- Sbatch Started --*/
							logger.Info("SLURM --> New Pod IP", "path", file)

						case ContainerJobIdExtension:
							/*-- Container Started --*/
							logger.Info("SLURM --> New Job ID", "path", file)

						case ContainerExitCodeExtension:
							/*-- Container Terminated --*/
							logger.Info("SLURM --> New Exit Code", "path", file)

						default:
							/*-- Anything else is ignored --*/
							logger.V(7).Info("Ignore fsnotify event", "op", "write", "file", file)

							continue
						}

						if err := reconcilePodStatus(podKey); err != nil {
							logger.Error(err, "failed to reconcile pod status")
						}
					}
				}
			}
		}()
		waitGroup.Wait()
	}
}

func reconcilePodStatus(podKey client.ObjectKey) error {
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
