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

package provider

import (
	"context"
	"io"
	"os"
	"time"

	"github.com/virtual-kubelet/virtual-kubelet/node/api/statsv1alpha1"

	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"

	"github.com/carv-ics-forth/hpk/compute"
	"github.com/carv-ics-forth/hpk/compute/slurm"
	"github.com/carv-ics-forth/hpk/pkg/filenotify"
	"github.com/go-logr/logr"
	"github.com/niemeyer/pretty"
	"github.com/pkg/errors"
	"github.com/virtual-kubelet/virtual-kubelet/errdefs"
	vkapi "github.com/virtual-kubelet/virtual-kubelet/node/api"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/json"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

// InitConfig is the config passed to initialize a registered provider.
type InitConfig struct {
	InternalIP string
	DaemonPort int32

	BuildVersion string

	FSPollingInterval time.Duration

	RestConfig *rest.Config
}

// VirtualK8S implements the virtual-kubelet provider interface and stores pods in memory.
type VirtualK8S struct {
	InitConfig

	Logger logr.Logger

	fileWatcher filenotify.FileWatcher
	updatedPod  func(*corev1.Pod)
}

// NewVirtualK8S reads a kubeconfig file and sets up a client to interact
// with Slurm cluster.
func NewVirtualK8S(config InitConfig) (*VirtualK8S, error) {
	var err error
	var watcher filenotify.FileWatcher
	logger := zap.New(zap.UseDevMode(true))

	if config.FSPollingInterval > 0 {
		watcher = filenotify.NewPollingWatcher(config.FSPollingInterval)
	} else {
		watcher, err = filenotify.NewEventWatcher()
	}

	if err != nil {
		return nil, errors.Wrapf(err, "add watcher on fsnotify failed")
	}

	/*---------------------------------------------------
	 * Restore missing state after a restart
	 *---------------------------------------------------*/
	// create the ~/.hpk directory, if it does not exist.
	if err := os.MkdirAll(compute.RuntimeDir, compute.PodGlobalDirectoryPermissions); err != nil {
		return nil, errors.Wrapf(err, "Failed to create RuntimeDir '%s'", compute.RuntimeDir)
	}

	// iterate the filesystem and restore fsnotify watchers
	if err := slurm.WalkPodDirectories(func(path compute.PodPath) error {
		return watcher.Add(path.String())
	}); err != nil {
		return nil, errors.Wrapf(err, "failed to restore watchers")
	}

	return &VirtualK8S{
		InitConfig:  config,
		Logger:      logger,
		fileWatcher: watcher,
	}, nil
}

/************************************************************

		Implements node.PodLifecycleHandler


PodLifecycleHandler defines the interface used by the PodController to react
to new and changed pods scheduled to the node that is being managed.

Errors produced by these methods should implement an interface from
github.com/virtual-kubelet/virtual-kubelet/errdefs package in order for the
core logic to be able to understand the type of failure.
************************************************************/

// CreatePod takes a Kubernetes Pod and deploys it within the provider.
func (v *VirtualK8S) CreatePod(ctx context.Context, pod *corev1.Pod) error {
	podKey := client.ObjectKeyFromObject(pod)
	logger := v.Logger.WithValues("obj", podKey)

	/*---------------------------------------------------
	 * Preamble used for Request tracing on the logs
	 *---------------------------------------------------*/
	logger.Info("K8s -> CreatePod")

	defer func() {
		logger.Info("K8s <- CreatePod")
	}()

	/*---------------------------------------------------
	 * Compromise with Virtual Kubernetes Conventions
	 *---------------------------------------------------*/

	/*
		Match the IPs between host and pod.
		This is needed so that node-level logging requests
		will be handled by the Virtual Kubelet
		https://ritazh.com/understanding-kubectl-logs-with-virtual-kubelet-a135e83ae0ee
	*/
	pod.Status.HostIP = v.InitConfig.InternalIP

	/*
		If an error is returned, Virtual Kubernetes will set the Phase to either "Pending" or "Failed",
		depending on the pod.RestartPolicy.
		In both cases, it will	wrap the reason into "podStatusReasonProviderFailed"
	*/
	if err := slurm.CreatePod(ctx, pod, v.fileWatcher); err != nil {
		return err
	}

	return nil
}

// UpdatePod takes a Kubernetes Pod and updates it within the provider.
func (v *VirtualK8S) UpdatePod(ctx context.Context, pod *corev1.Pod) error {
	podKey := client.ObjectKeyFromObject(pod)
	logger := v.Logger.WithValues("obj", podKey)

	/*---------------------------------------------------
	 * Preamble used for Request tracing on the logs
	 *---------------------------------------------------*/
	logger.Info("K8s -> UpdatePod",
		"version", pod.GetResourceVersion(),
		"phase", pod.Status.Phase,
	)

	defer logger.Info("K8s <- UpdatePod")

	/*---------------------------------------------------
	 * Ensure that received pod is newer than the local
	 *---------------------------------------------------*/
	localPod := slurm.LoadPod(podKey)
	if localPod == nil {
		return errdefs.NotFoundf("object not found")
	}

	logger.Info("Local Pod ",
		"version", localPod.GetResourceVersion(),
		"phase", localPod.Status.Phase,
	)

	if localPod.ResourceVersion >= pod.ResourceVersion {
		/*-- The received pod is old, so we can safely discard it --*/
		logger.Info("Discard update since its ResourceVersion is older than the local")

		return nil
	}

	/*---------------------------------------------------
	 * Identify any intermediate actions that must taken
	 *---------------------------------------------------*/
	if metaDiff := pretty.Diff(localPod.ObjectMeta, pod.ObjectMeta); len(metaDiff) > 0 {
		/* ... */
	}

	if specDiff := pretty.Diff(localPod.Spec, pod.Spec); len(specDiff) > 0 {
		/* ... */
	}

	if statusDiff := pretty.Diff(localPod.Status, pod.Status); len(statusDiff) > 0 {
		/* ... */
	}

	/*-- Update the local status of Pod --*/
	slurm.SavePod(ctx, pod)

	logger.Info("Update pod success")

	return nil
}

// DeletePod takes a Kubernetes Pod and deletes it from the provider. Once a pod is deleted, the provider is
// expected to call the NotifyPods callback with a terminal pod status where all the containers are in a terminal
// state, as well as the pod. DeletePod may be called multiple times for the same pod.
func (v *VirtualK8S) DeletePod(ctx context.Context, pod *corev1.Pod) error {
	podKey := client.ObjectKeyFromObject(pod)
	logger := v.Logger.WithValues("obj", podKey)

	/*---------------------------------------------------
	 * Preamble used for Request tracing on the logs
	 *---------------------------------------------------*/
	logger.Info("K8s -> DeletePod")

	if !slurm.DeletePod(podKey, v.fileWatcher) {
		logger.Info("K8s <- DeletePod (POD NOT FOUND)")

		return errdefs.NotFoundf("object not found")
	}

	/*---------------------------------------------------
	 * Mark Containers and Pod as Terminated
	 *---------------------------------------------------*/
	/*
		pod.Status.Phase = corev1.PodSucceeded
		ready := false

		for i := range pod.Status.InitContainerStatuses {
			pod.Status.InitContainerStatuses[i].Started = &ready
			pod.Status.InitContainerStatuses[i].Ready = ready
			pod.Status.InitContainerStatuses[i].State.Terminated = &corev1.ContainerStateTerminated{
				Reason:     "PodIsDeleted",
				Message:    "Pod is being deleted",
				FinishedAt: metav1.Now(),
			}
		}

		for i := range pod.Status.ContainerStatuses {
			pod.Status.ContainerStatuses[i].Started = &ready
			pod.Status.ContainerStatuses[i].Ready = ready
			pod.Status.ContainerStatuses[i].State.Terminated = &corev1.ContainerStateTerminated{
				Reason:     "PodIsDeleted",
				Message:    "Pod is being deleted",
				FinishedAt: metav1.Now(),
			}
		}

		v.updatedPod(pod)

	*/

	logger.Info("K8s <- DeletePod (SUCCESS)")
	return nil
}

// GetPod retrieves a pod by name from the provider (can be cached).
// The Pod returned is expected to be immutable, and may be accessed
// concurrently outside the calling goroutine. Therefore, it is recommended
// to return a version after DeepCopy.
func (v *VirtualK8S) GetPod(ctx context.Context, namespace, name string) (*corev1.Pod, error) {
	podKey := client.ObjectKey{Namespace: namespace, Name: name}
	logger := v.Logger.WithValues("obj", podKey)

	/*---------------------------------------------------
	 * Preamble used for Request tracing on the logs
	 *---------------------------------------------------*/
	logger.Info("K8s -> GetPod")

	pod := slurm.LoadPod(podKey)
	if pod == nil {
		logger.Info("K8s <- GetPod (POD NOT FOUND)")

		return nil, errdefs.NotFoundf("object not found")
	}

	logger.Info("K8s <- GetPod",
		"version", pod.GetResourceVersion(),
		"phase", pod.Status.Phase,
	)

	return pod.DeepCopy(), nil
}

// GetPodStatus retrieves the status of a pod by name from the provider.
// The PodStatus returned is expected to be immutable, and may be accessed
// concurrently outside the calling goroutine. Therefore, it is recommended
// to return a version after DeepCopy.
func (v *VirtualK8S) GetPodStatus(ctx context.Context, namespace, name string) (*corev1.PodStatus, error) {
	podKey := client.ObjectKey{Namespace: namespace, Name: name}
	logger := v.Logger.WithValues("obj", podKey)

	/*---------------------------------------------------
	 * Preamble used for Request tracing on the logs
	 *---------------------------------------------------*/
	logger.Info("K8s -> GetPodStatus")

	pod := slurm.LoadPod(podKey)
	if pod == nil {
		logger.Info("K8s <- GetPodStatus (POD NOT FOUND)")

		return nil, errdefs.NotFoundf("object not found")
	}

	logger.Info("K8s <- GetPodStatus",
		"version", pod.GetResourceVersion(),
		"phase", pod.Status.Phase,
	)

	return pod.Status.DeepCopy(), nil
}

// GetPods retrieves a list of all pods running on the provider (can be cached).
// The Pods returned are expected to be immutable, and may be accessed
// concurrently outside the calling goroutine. Therefore, it is recommended
// to return a version after DeepCopy.
func (v *VirtualK8S) GetPods(ctx context.Context) ([]*corev1.Pod, error) {
	/*---------------------------------------------------
	 * Preamble used for Request tracing on the logs
	 *---------------------------------------------------*/
	v.Logger.Info("K8s -> GetPods")
	defer v.Logger.Info("K8s <- GetPods")

	/*---------------------------------------------------
	 * Iterate the filesystem and extract local pods
	 *---------------------------------------------------*/
	var pods []*corev1.Pod

	if err := slurm.WalkPodDirectories(func(path compute.PodPath) error {
		encodedPod, err := os.ReadFile(path.EncodedJSONPath())
		if err != nil {
			v.Logger.Error(err, "potentially corrupted dir. cannot read pod description file",
				"path", path)

			return nil
		}

		var pod corev1.Pod

		if err := json.Unmarshal(encodedPod, &pod); err != nil {
			return errors.Wrapf(err, "cannot decode pod description file '%s'", path)
		}

		/*-- return only the pods that are known to be running --*/
		if pod.Status.Phase == corev1.PodRunning {
			pods = append(pods, &pod)
		}

		return nil
	}); err != nil {
		return nil, errors.Wrapf(err, "failed to traverse pods")
	}

	return pods, nil
}

// NotifyPods instructs the notifier to call the passed in function when
// the pod status changes. It should be called when a pod's status changes.
//
// The provided pointer to a Pod is guaranteed to be used in a read-only
// fashion. The provided pod's PodStatus should be up to date when
// this function is called.
//
// NotifyPods must not block the caller since it is only used to register the callback.
// The callback passed into `NotifyPods` may block when called.
func (v *VirtualK8S) NotifyPods(_ context.Context, f func(*corev1.Pod)) {
	/*---------------------------------------------------
	 * Preamble used for Request tracing on the logs
	 *---------------------------------------------------*/
	v.Logger.Info("K8s -> NotifyPods")
	defer v.Logger.Info("K8s <- NotifyPods")

	/*---------------------------------------------------
	 * Listen for Slurm Events caused by Pods.
	 *---------------------------------------------------*/
	v.updatedPod = f

	/*-- start event handler --*/
	eh := slurm.NewEventHandler(slurm.Options{
		MaxWorkers:   1,
		MaxQueueSize: 10,
	})

	go eh.Run(context.Background(), func(pod *corev1.Pod) {
		if pod == nil {
			panic("this should not happen")
		}

		f(pod)
	})

	/*-- add fileWatcher events to queue to be processed asynchronously --*/
	go func() {
		for {
			select {
			case event, ok := <-v.fileWatcher.Events():
				if !ok {
					return
				}
				eh.Push(event)

			case err, ok := <-v.fileWatcher.Errors():
				if !ok {
					return
				}

				panic(errors.Wrapf(err, "fsnotify failed"))
			}
		}
	}()
}

/************************************************************

		Implements vkapi.VirtualK8S

************************************************************/

func (v *VirtualK8S) GetStatsSummary(context.Context) (*statsv1alpha1.Summary, error) {
	/*---------------------------------------------------
	 * Preamble used for Request tracing on the logs
	 *---------------------------------------------------*/
	v.Logger.Info("K8s -> GetStatsSummary")
	defer v.Logger.Info("K8s <- GetStatsSummary")

	panic("not yet supported")
}

// GetContainerLogs retrieves the logs of a container by name from the provider.
func (v *VirtualK8S) GetContainerLogs(ctx context.Context, namespace, podName, containerName string, opts vkapi.ContainerLogOpts) (io.ReadCloser, error) {
	podKey := client.ObjectKey{Namespace: namespace, Name: podName}
	logger := v.Logger.WithValues("obj", podKey)

	/*---------------------------------------------------
	 * Preamble used for Request tracing on the logs
	 *---------------------------------------------------*/
	logger.Info("K8s -> GetContainerLogs", "container", containerName)

	pod := slurm.LoadPod(podKey)
	if pod == nil {
		logger.Info("K8s <- GetContainerLogs (POD NOT FOUND)")

		return nil, errdefs.NotFoundf("object not found")
	}

	podDir := compute.PodRuntimeDir(podKey)

	logfile := podDir.Container(containerName).LogsPath()

	logger.Info("K8s <- GetContainerLogs", "logfile", logfile)

	// if it fails to open the container's logfile, then may be something with the pod.
	// in this case, try return the pod stderr
	containerLog, err := os.Open(logfile)
	if err != nil {
		logger.Info("Unable to find container's log. Fallback to pod's stderr")

		podStderr, err := os.Open(podDir.StderrPath())
		if err != nil {
			return nil, errors.Wrapf(err, "unable to load either container's or pod's logs")
		}

		return podStderr, nil
	}

	return containerLog, nil
}

// RunInContainer executes a command in a container in the pod, copying data
// between in/out/err and the container's stdin/stdout/stderr.
func (v *VirtualK8S) RunInContainer(ctx context.Context, namespace, podName, containerName string, cmd []string, attach vkapi.AttachIO) error {
	podKey := client.ObjectKey{Namespace: namespace, Name: podName}
	logger := v.Logger.WithValues("obj", podKey)

	/*---------------------------------------------------
	 * Preamble used for Request tracing on the logs
	 *---------------------------------------------------*/
	logger.Info("K8s -> RunInContainer", "container", containerName)
	defer logger.Info("K8s <- RunInContainer", "container", containerName)

	defer func() {
		if attach.Stdout() != nil {
			attach.Stdout().Close()
		}
		if attach.Stderr() != nil {
			attach.Stderr().Close()
		}
	}()

	req := compute.K8SClientset.RESTClient().
		Post().
		Namespace(namespace).
		Resource("pods").
		Name(podName).
		SubResource("exec").
		Timeout(0).
		VersionedParams(&corev1.PodExecOptions{
			Container: containerName,
			Command:   cmd,
			Stdin:     attach.Stdin() != nil,
			Stdout:    attach.Stdout() != nil,
			Stderr:    attach.Stderr() != nil,
			TTY:       attach.TTY(),
		}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(v.InitConfig.RestConfig, "POST", req.URL())
	if err != nil {
		return errors.Wrapf(err, "could not make remote command")
	}

	return exec.Stream(remotecommand.StreamOptions{
		Stdin:             attach.Stdin(),
		Stdout:            attach.Stdout(),
		Stderr:            attach.Stderr(),
		Tty:               attach.TTY(),
		TerminalSizeQueue: &termSize{attach: attach},
	})
}

// termSize helps exec termSize
type termSize struct {
	attach vkapi.AttachIO
}

// Next returns the new terminal size after the terminal has been resized. It returns nil when
// monitoring has been stopped.
func (t *termSize) Next() *remotecommand.TerminalSize {
	resize := <-t.attach.Resize()
	return &remotecommand.TerminalSize{
		Height: resize.Height,
		Width:  resize.Width,
	}
}
