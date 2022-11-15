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
	"regexp"
	"sync"

	"github.com/carv-ics-forth/hpk/pkg/process"
	"github.com/fsnotify/fsnotify"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
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

	ExtensionIP  = ".ip"
	ExtensionCRD = ".crd"

	ExtensionExitCode = ".exitCode"
	ExtensionJobID    = ".jid"

	ExtensionStdout = ".stdout"
	ExtensionStderr = ".stderr"
)

type PodPath string

func PodRuntimeDir(podRef client.ObjectKey) PodPath {
	path := filepath.Join(RuntimeDir, podRef.Namespace, podRef.Name)

	return PodPath(path)
}

func (p PodPath) EncodedJSONPath() string {
	return filepath.Join(string(p), ExtensionCRD)
}

// Mountpaths returns .hpk/namespace/podName/mountName:mountPath
func (p PodPath) Mountpaths(mount corev1.VolumeMount) string {
	return filepath.Join(string(p), mount.Name+":"+mount.MountPath)
}

// IDPath .hpk/namespace/podName/.jid
func (p PodPath) IDPath() string {
	return filepath.Join(string(p), ExtensionJobID)
}

// ExitCodePath .hpk/namespace/podName/.exitCode
func (p PodPath) ExitCodePath() string {
	return filepath.Join(string(p), ExtensionExitCode)
}

// VirtualEnvironmentDir .hpk/namespace/podName/.venv
func (p PodPath) VirtualEnvironmentDir() PodPath {
	return PodPath(filepath.Join(string(p), ".venv"))
}

// ConstructorPath .hpk/namespace/podName/.venv/constructor.sh
func (p PodPath) ConstructorPath() string {
	return filepath.Join(string(p.VirtualEnvironmentDir()), "constructor.sh")
}

// SubmitJobPath .hpk/namespace/podName/.venv/submit.sh
func (p PodPath) SubmitJobPath() string {
	return filepath.Join(string(p.VirtualEnvironmentDir()), "submit.sh")
}

// IPAddressPath .hpk/namespace/podName/.venv/pod.ip
func (p PodPath) IPAddressPath() string {
	return filepath.Join(string(p.VirtualEnvironmentDir()), "pod"+ExtensionIP)
}

// StdoutPath .hpk/namespace/podName/.venv/pod.stdout
func (p PodPath) StdoutPath() string {
	return filepath.Join(string(p.VirtualEnvironmentDir()), "pod"+ExtensionStdout)
}

// StderrPath .hpk/namespace/podName/.venv/pod.stderr
func (p PodPath) StderrPath() string {
	return filepath.Join(string(p.VirtualEnvironmentDir()), "pod"+ExtensionStderr)
}

func (p PodPath) CreateSubDirectory(name string) (string, error) {
	fullPath := filepath.Join(string(p), name)

	if err := os.MkdirAll(fullPath, PodGlobalDirectoryPermissions); err != nil {
		return fullPath, errors.Wrapf(err, "cannot create dir '%s'", fullPath)
	}

	return fullPath, nil
}

func (p PodPath) Container(containerName string) ContainerPath {
	return ContainerPath(filepath.Join(string(p), containerName))
}

type ContainerPath string

func (c ContainerPath) StdoutPath() string {
	return filepath.Join(string(c) + ExtensionStdout)
}

func (c ContainerPath) StderrPath() string {
	return filepath.Join(string(c) + ExtensionStderr)
}

func (c ContainerPath) IDPath() string {
	return filepath.Join(string(c) + ExtensionJobID)
}

func (c ContainerPath) ExitCodePath() string {
	return filepath.Join(string(c) + ExtensionExitCode)
}

// ParseINotifyPath parses the path according to the expected HPK format, and returns
// the corresponding fields.
// Validated through: https://regex101.com/r/s4tb8x/1
func ParseINotifyPath(path string) (podKey types.NamespacedName, fileName string) {
	re := regexp.MustCompile(`^.hpk/(?P<namespace>\w+)/(?P<pod>.*?)(/.venv)*/(?P<file>.*)$`)

	match := re.FindStringSubmatch(path)

	if len(match) == 0 {
		panic(errors.Errorf("path '%s' does not follow the HPC convention", path))
	}

	for i, name := range re.SubexpNames() {
		if i > 0 && i <= len(match) {
			switch name {
			case "namespace":
				podKey.Namespace = match[i]
			case "pod":
				podKey.Name = match[i]
			case "file":
				fileName = match[i]
			}
		}
	}

	return
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

						podkey, file := ParseINotifyPath(event.Name)

						logger := DefaultLogger.WithValues("pod", podkey)

						/*---------------------------------------------------
						 * Declare events that warrant VirtualEnvironment reconciliation
						 *---------------------------------------------------*/
						switch filepath.Ext(file) {
						case ExtensionIP:
							/*-- Sbatch Started --*/
							logger.Info("SLURM --> New VirtualEnvironment IP", "path", file)

						case ExtensionJobID:
							/*-- Process Started --*/
							logger.Info("SLURM --> New Job IDPath", "path", file)

						case ExtensionExitCode:
							/*-- Process Terminated --*/
							logger.Info("SLURM --> New Exit Code", "path", file)

						default:
							/*-- Anything else is ignored --*/
							logger.V(7).Info("Ignore fsnotify event", "op", "write", "file", file)

							continue
						}

						if err := reconcilePodStatus(podkey); err != nil {
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
	/*-- Load VirtualEnvironment from reference --*/
	pod, err := GetPod(podKey)
	if err != nil {
		return errors.Wrapf(err, "unable to load pod")
	}

	/*-- Recalculate the VirtualEnvironment status from locally stored containers --*/
	if err := podStateMapper(pod); err != nil {
		return errors.Wrapf(err, "unable to update pod status")
	}

	/*-- Update the top-level VirtualEnvironment description --*/
	if err := SavePod(pod); err != nil {
		return errors.Wrapf(err, "unable to store pod")
	}

	return nil
}
