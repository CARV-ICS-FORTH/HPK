package hpc

import (
	"context"
	"path/filepath"
	"strings"
	"sync"

	"github.com/carv-ics-forth/knoc/api"
	"github.com/fsnotify/fsnotify"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
)

var ErrClosedQueue = errors.New("queue is closed")

type EventController interface {
	// Push takes an Event and pushes to a queue.
	Push(fsnotify.Event) error
	// Run spawns the workers and waits indefinitely for
	// the events to be processed.
	Run()
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

// Options represent options for FSEventDispatcher.
type Options struct {
	MaxWorkers   int // Number of workers to spawn.
	MaxQueueSize int // Maximum length for the queue to hold events.
}

// NewFSEventDispatcher initialises a new event dispatcher.
func NewFSEventDispatcher(opts Options) *FSEventDispatcher {
	return &FSEventDispatcher{
		Opts:     opts,
		Queue:    make(chan fsnotify.Event, opts.MaxQueueSize),
		Finished: false,
	}
}

// Push adds a new event payload to the queue.
func (d *FSEventDispatcher) Push(event fsnotify.Event) error {
	d.locker.RLock()
	defer d.locker.RUnlock()

	if d.Finished {
		return ErrClosedQueue
	}

	d.Queue <- event

	return nil
}

type InspectFS interface {
	GetPod(ctx context.Context, namespace string, podName string) (*corev1.Pod, error)
	UpdatePod(ctx context.Context, pod *corev1.Pod) error
}

// Run spawns workers and listens to the queue
// It's a blocking function and waits for a cancellation
// invocation from the Client.
func (d *FSEventDispatcher) Run(ctx context.Context, inspect InspectFS) {
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

						switch filepath.Ext(file) {
						case api.ExitCodeExtension:
							fields := strings.Split(dir, "/")
							namespace, podName := fields[1], fields[2]
							// containerName := strings.TrimSuffix(file, filepath.Ext(file))

							if err := updatePodStatus(ctx, inspect, namespace, podName); err != nil {
								logrus.Warn("Update pod error")
							}

						case api.JobIdExtension:
							fields := strings.Split(dir, "/")
							namespace, podName := fields[1], fields[2]

							if err := updatePodStatus(ctx, inspect, namespace, podName); err != nil {
								logrus.Warn("Update pod error")
							}
						}
					}
				}
			}
		}()
		waitGroup.Wait()
	}
}
