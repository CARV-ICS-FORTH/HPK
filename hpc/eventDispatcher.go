package hpc

import (
	"context"
	"github.com/sirupsen/logrus"
	"log"
	"path/filepath"
	"strings"
	"sync"

	"github.com/pkg/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/carv-ics-forth/knoc/api"
	"github.com/fsnotify/fsnotify"
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

type Inspector interface {
	GetPod(ctx context.Context, namespace string, podName string) (*corev1.Pod, error)
	UpdatePod(ctx context.Context, pod *corev1.Pod) error
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
					if event.Op == fsnotify.Write {
						dir, file := filepath.Split(event.Name)
						switch filepath.Ext(file) {
						case api.ExitCodeExtension:
							{
								log.Println("A container stopped:", event.Name)
								pod, _, containerStatus := findPodAndContainerStatus(ctx, dir, file, inspect)

								_ = pod
								_ = containerStatus

								// check exitCode
								// if exit ok
								//	update container status to termination
								// if last container of pod exited then update PodStatus

								// if last initContainer exited then update PodCondition to initialized

								// if any container exited with non-zero exit code update pod status to PodFailed
								if err := inspect.UpdatePod(ctx, pod); err != nil {
									panic(err)
								}
							}
						case api.JobIdExtension:
							{
								log.Println("A container started by a slurm job:", event.Name)

								pod, container, containerStatus := findPodAndContainerStatus(ctx, dir, file, inspect)

								_ = pod
								_ = containerStatus

								//if pod.Status.Phase == corev1.PodSucceeded ||
								//	pod.Status.Phase == corev1.PodFailed ||
								//	pod.Status.Phase == corev1.PodPending {
								//	panic("should not be here")
								//}

								addRunningContainerState(container, pod)
								// need to update pod status
								// need to add pod conditions

								if err := inspect.UpdatePod(ctx, pod); err != nil {
									panic(err)
								}
							}
						}
					}
				}
			}
		}()
		waitGroup.Wait()
	}
}

// TODO: should go to provider
func addRunningContainerState(container *corev1.Container, pod *corev1.Pod) {
	now := metav1.Now()
	pod.Status.ContainerStatuses = append(pod.Status.ContainerStatuses, corev1.ContainerStatus{
		Name:         container.Name,
		Image:        container.Image,
		Ready:        true,
		RestartCount: 1,
		State: corev1.ContainerState{
			Running: &corev1.ContainerStateRunning{
				StartedAt: now,
			},
		},
	})
}

// TODO: should go to provider
// find container with given podName and containerName
func findPodAndContainerStatus(ctx context.Context, dir string, file string, inspect Inspector) (*corev1.Pod, *corev1.Container, *corev1.ContainerStatus) {
	fields := strings.Split(dir, "/")
	namespace := fields[1]
	podName := fields[2]
	containerName := strings.TrimSuffix(file, filepath.Ext(file))
	logrus.Warn(namespace)
	logrus.Warn(podName)
	logrus.Warn(containerName)

	//	find container with given podName and containerName
	pod, err := inspect.GetPod(ctx, namespace, podName)

	if err != nil || pod == nil {
		panic(errors.Wrapf(err, "Pod in runtime directory was not found for container '%s'", containerName))
	}

	var targetContainer *corev1.Container
	var targetContainerStatus *corev1.ContainerStatus
	// find out if the container is initContainer or main container
	for index, initContainer := range pod.Spec.InitContainers {
		if initContainer.Name == containerName {
			targetContainerStatus = &pod.Status.InitContainerStatuses[index]
			targetContainer = &pod.Spec.InitContainers[index]
		}
	}

	// find out if the container is initContainer or main container
	for index, mainContainer := range pod.Spec.Containers {
		if mainContainer.Name == containerName {
			targetContainerStatus = &pod.Status.ContainerStatuses[index]
			targetContainer = &pod.Spec.Containers[index]
		}
	}

	if targetContainerStatus == nil {
		panic(errors.Errorf("container status not found for container '%s'", containerName))
	}

	return pod, targetContainer, targetContainerStatus
}
