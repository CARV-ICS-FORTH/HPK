package hpc

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/carv-ics-forth/knoc/api"
	"github.com/carv-ics-forth/knoc/pkg/ui"
	"github.com/fsnotify/fsnotify"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

/************************************************************

			Find SLURM Executables

************************************************************/

var Executables = Environment{
	sbatchExecutablePath:      checkExistenceOrDie(SBATCH),
	scancelExecutablePath:     checkExistenceOrDie(SCANCEL),
	singularityExecutablePath: checkExistenceOrDie(SINGULARITY),
}

const (
	SBATCH      = "sbatch"
	SCANCEL     = "scancel"
	SINGULARITY = "singularity"
	MPIEXEC     = "mpiexec"
)

type Environment struct {
	sbatchExecutablePath      string
	scancelExecutablePath     string
	singularityExecutablePath string
	mpiexecExecutablePath     string
}

func (hpc *Environment) MpiexecPath() string {
	return hpc.mpiexecExecutablePath
}

func (hpc *Environment) SingularityPath() string {
	return hpc.singularityExecutablePath
}

func (hpc *Environment) Scancel(args string) (string, error) {
	output, err := exec.Command(hpc.scancelExecutablePath, args).Output()
	if err != nil {
		return "", errors.Wrap(err, "Could not run scancel")
	}

	return string(output), nil
}

func (hpc *Environment) SBatchFromFile(ctx context.Context, path string) (string, error) {
	output, err := exec.CommandContext(ctx, hpc.sbatchExecutablePath, path).Output()
	if err != nil {
		return "", errors.Wrapf(err, "sbatch execution error.")
	}

	return string(output), nil
}

func (hpc *Environment) SbatchMacros(instanceName string, sbatchFlags string) string {
	return "#!/bin/bash" +
		"\n#SBATCH --job-name=" + instanceName +
		sbatchFlags +
		"\n. ~/.bash_profile" +
		"\npwd; hostname; date\n"
}

// checkExistenceOrDie searches for an executable named binary in the directories named by the PATH environment variable
// and returns the binary's path, otherwise panics
func checkExistenceOrDie(executableName string) string {
	// find the executable
	path, err := exec.LookPath(executableName)
	if err != nil {
		ui.Fail(err)
	}

	// check that we can access the executable (permissions, etc etc)
	if _, err := os.Stat(path); err != nil {
		ui.Fail(err)
	}

	logrus.Infof("Found executable '%s' -> '%s'", executableName, path)

	return path
}

/************************************************************

			Listen for SLURM events

************************************************************/

var fswatcher *fsnotify.Watcher

var fsEventHandler = FSEventDispatcher{
	Opts: Options{
		MaxWorkers:   api.DefaultMaxWorkers,
		MaxQueueSize: api.DefaultMaxQueueSize,
	},
	Queue:    make(chan fsnotify.Event, api.DefaultMaxQueueSize),
	locker:   sync.RWMutex{},
	Finished: false,
}

func init() {
	go fsEventHandler.Run(context.Background())

	// Create new watcher.
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		panic(errors.Wrap(err, "cannot create an fsnotify watcher"))
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
		defaultLogger.Error(ErrClosedQueue, "event queue is closed")
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

						logger := defaultLogger.WithValues("pod", podKey)

						switch filepath.Ext(file) {
						case api.JobIdExtension: /* <---- Slurm Job Creation / Pod Scheduling */
							logger.Info("Event: Slurm Job Started", "file", file)

							if err := renewPodStatus(ctx, podKey); err != nil {
								logger.Error(err, "failed to handle job creation event")
							}

						case api.ExitCodeExtension: /*-- Slurm Job Completion / Pod Termination --*/
							logger.Info("Event: Slurm Job Completed", "file", file)

							if err := renewPodStatus(ctx, podKey); err != nil {
								logger.Error(err, "failed to handle job completion event")
							}

						default:
							logger.Info("Ignore fsnotify event", "op", "write", "file", file)
						}
					}
				}
			}
		}()
		waitGroup.Wait()
	}
}
