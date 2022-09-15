package hpc

import (
	"context"
	"errors"
	"github.com/carv-ics-forth/knoc/api"
	"github.com/fsnotify/fsnotify"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	"log"
	"path/filepath"
	"strings"
	"sync"
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

type Inspector interface {
	GetPod(ctx context.Context, namespace string, podName string) (*corev1.Pod, error)
}

// Run spawns workers and listens to the queue
// It's a blocking function and waits for a cancellation
// invocation from the Client.
func (d *FSEventDispatcher) Run(ctx context.Context, inspect Inspector) {
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
					switch event.Op {
					case fsnotify.Write:
						dir, file := filepath.Split(event.Name)

						if filepath.Ext(file) == api.ExitCodeExtension {
							log.Println("Modified file:", event.Name)

							//fixme: ...

							fields := strings.Split(dir, "/")
							logrus.Warn("FIELDS ", fields)

							namespace := fields[1]
							podName := fields[2]

							//	find container with given podName and containerName
							pod, err := inspect.GetPod(ctx, namespace, podName)
							if err != nil {
								panic(err)
							}
							logrus.Warn(pod.String())
							_ = pod
							//pod.Spec.Containers
							// mark the container termination status
							// accordingly characterize pod
						}
					}
				}
			}
		}()
	}
	waitGroup.Wait()
}
