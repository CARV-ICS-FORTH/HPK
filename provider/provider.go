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
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/carv-ics-forth/hpk/compute/endpoint"
	"github.com/carv-ics-forth/hpk/compute/events"
	PodHandler "github.com/carv-ics-forth/hpk/compute/podhandler"
	"github.com/carv-ics-forth/hpk/compute/runtime"
	"github.com/carv-ics-forth/hpk/compute/slurm"
	"github.com/carv-ics-forth/hpk/pkg/container"
	"github.com/sirupsen/logrus"
	"github.com/virtual-kubelet/virtual-kubelet/node/api/statsv1alpha1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"

	"github.com/carv-ics-forth/hpk/compute"
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
// with Slurm cluster. It is designed to restore missing state after a restart.
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
	 * Initialize HPK Environment
	 *---------------------------------------------------*/
	if err := runtime.Initialize(); err != nil {
		return nil, errors.Wrapf(err, "Failed to initiaze HPK paths '%s'", compute.HPK.String())
	}

	/*---------------------------------------------------
	 * Handle Corrupted Pods (With missing state)
	 *---------------------------------------------------*/
	var corruptedPods []endpoint.PodPath

	// move corrupted pods to a centralized dir for inspection.
	// Valid are considered the pods with a Pod description.
	if err := compute.HPK.WalkPodDirectories(func(podpath endpoint.PodPath) error {
		ok, info := podpath.PodEnvironmentIsOK()
		if !ok {
			corruptedPods = append(corruptedPods, podpath)

			compute.DefaultLogger.Info("Corrupted Pod has been detected",
				"reason", info,
				"path", podpath,
			)
		}

		return nil
	}); err != nil {
		return nil, errors.Wrapf(err, "pod scanning has failed")
	}

	for _, path := range corruptedPods {
		archivedPodPath := strings.ReplaceAll(string(path), compute.HPK.String(), compute.HPK.CorruptedDir())

		// ensure that path prefix exists.
		archivedNamespacePath := filepath.Dir(archivedPodPath)

		if err := os.MkdirAll(archivedNamespacePath, endpoint.PodGlobalDirectoryPermissions); err != nil {
			return nil, errors.Wrapf(err, "basepath '%s' error", archivedNamespacePath)
		}

		// move corrupted pod to the archived namespace.
	retry:
		if err := os.Rename(string(path), archivedPodPath); err != nil {
			if errors.Is(err, os.ErrExist) {
				// retry to rename the pod
				archivedPodPath = fmt.Sprintf("%s-%d", archivedPodPath, time.Now().Second())
				goto retry
			}
			return nil, errors.Wrapf(err, "moving error of corrupted pod")
		}

		logger.Info("Moved corrupted pod to archive",
			"from", path,
			"to", archivedPodPath,
		)
	}

	/*---------------------------------------------------
	 * Set fsnotify watchers for Pods
	 *---------------------------------------------------*/
	if err := compute.HPK.WalkPodDirectories(func(path endpoint.PodPath) error {
		// register the watcher
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

	logger.Info("[K8s] -> CreatePod")

	defer func() {
		logger.Info("[K8s] <- CreatePod")
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

	/*---------------------------------------------------
	 * Create the pod Asynchronously.
	 *---------------------------------------------------*/
	go func() {
		// acknowledge the creation request and do the creation in the background.
		// if the creation fails, the pod should be marked as failed and returned to the provider.
		PodHandler.CreatePod(ctx, pod, v.fileWatcher)

		v.updatedPod(pod)
	}()

	/*
		If an error is returned, Virtual Kubernetes will set the Phase to either "Pending" or "Failed",
		depending on the pod.RestartPolicy.
		In both cases, it will	wrap the reason into "podStatusReasonProviderFailed"
	*/
	return nil
}

// UpdatePod takes a Kubernetes Pod and updates it within the provider.
func (v *VirtualK8S) UpdatePod(ctx context.Context, pod *corev1.Pod) error {
	podKey := client.ObjectKeyFromObject(pod)
	logger := v.Logger.WithValues("obj", podKey)

	logger.Info("[K8s] -> UpdatePod",
		"version", pod.GetResourceVersion(),
		"phase", pod.Status.Phase,
	)

	defer logger.Info("[K8s] <- UpdatePod")

	/*---------------------------------------------------
	 * Ensure that received pod is newer than the local
	 *---------------------------------------------------*/
	localPod, err := PodHandler.LoadPodFromKey(podKey)
	if err != nil {
		return errdefs.NotFoundf("object not found")
	}

	if localPod.ResourceVersion >= pod.ResourceVersion {
		// The received pod is old, so we can safely discard it
		logger.Info("Discard update since its ResourceVersion is older than the local")

		return nil
	}

	if !slurm.HasJobID(pod) {
		// If the pod has not a received a job id, it means that it still being in the Slurm queue.
		logger.Info("Discard update because job is still in the queue")
		return nil
	}

	/*---------------------------------------------------
	 * Identify any intermediate actions that must taken
	 *---------------------------------------------------*/
	if metaDiff := pretty.Diff(localPod.ObjectMeta.Annotations, pod.ObjectMeta.Annotations); len(metaDiff) > 0 {
		/* ... */
		logrus.Warn("DIFFERENCES ", metaDiff)
	}

	if specDiff := pretty.Diff(localPod.Spec, pod.Spec); len(specDiff) > 0 {
		/* ... */
	}

	if statusDiff := pretty.Diff(localPod.Status, pod.Status); len(statusDiff) > 0 {
		/* ... */
	}

	/*-- Update the local status of Pod --*/
	if err := PodHandler.SavePodToFile(ctx, pod); err != nil {
		compute.SystemPanic(err, "failed to set job id for pod '%s'", podKey)
	}

	return nil
}

// DeletePod takes a Kubernetes Pod and deletes it from the provider. Once a pod is deleted, the provider is
// expected to call the NotifyPods callback with a terminal pod status where all the containers are in a terminal
// state, as well as the pod. DeletePod may be called multiple times for the same pod.
func (v *VirtualK8S) DeletePod(ctx context.Context, pod *corev1.Pod) error {
	podKey := client.ObjectKeyFromObject(pod)
	logger := v.Logger.WithValues("obj", podKey)

	logger.Info("[K8s] -> DeletePod")

	if !PodHandler.DeletePod(podKey, v.fileWatcher) {
		logger.Info("[K8s] <- DeletePod (POD NOT FOUND)")

		return errdefs.NotFoundf("object not found")
	}

	logger.Info("[K8s] <- DeletePod (SUCCESS)")
	return nil
}

// GetPod retrieves a pod by name from the provider (can be cached).
// The Pod returned is expected to be immutable, and may be accessed
// concurrently outside the calling goroutine. Therefore, it is recommended
// to return a version after DeepCopy.
func (v *VirtualK8S) GetPod(ctx context.Context, namespace, name string) (*corev1.Pod, error) {
	podKey := client.ObjectKey{Namespace: namespace, Name: name}
	logger := v.Logger.WithValues("obj", podKey)

	logger.Info("[K8s] -> GetPod")

	pod, err := PodHandler.LoadPodFromKey(podKey)
	if err != nil {
		logger.Info("[K8s] <- GetPod (POD NOT FOUND)")

		return nil, errdefs.NotFoundf("object not found")
	}

	logger.Info("[K8s] <- GetPod",
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
	logger := v.Logger.WithValues("obj", podKey, "job")

	logger.Info("[K8s] -> GetPodStatus")

	pod, err := PodHandler.LoadPodFromKey(podKey)
	if err != nil {
		logger.Info("[K8s] <- GetPodStatus (POD NOT FOUND)")

		return nil, errdefs.NotFoundf("object not found")
	}

	logger.Info("[K8s] <- GetPodStatus",
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
	v.Logger.Info("[K8s] -> GetPods")
	defer v.Logger.Info("[K8s] <- GetPods")

	/*---------------------------------------------------
	 * Iterate the filesystem and extract local pods
	 *---------------------------------------------------*/
	var pods []*corev1.Pod

	if err := compute.HPK.WalkPodDirectories(func(path endpoint.PodPath) error {
		encodedPod, err := os.ReadFile(path.EncodedJSONPath())
		if err != nil {
			v.Logger.Info("Ignore Corrupted Pod Dir", "path", path, "error", err)

			// continue with the rest of pods
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
// fashion. The provided pod's PodStatus should be up-to-date when
// this function is called.
//
// NotifyPods must not block the caller since it is only used to register the callback.
// The callback passed into `NotifyPods` may block when called.
func (v *VirtualK8S) NotifyPods(ctx context.Context, f func(*corev1.Pod)) {
	v.Logger.Info("[K8s] -> NotifyPods")
	defer v.Logger.Info("[K8s] <- NotifyPods")

	/*---------------------------------------------------
	 * Listen for Slurm Events caused by Pods.
	 *---------------------------------------------------*/
	v.updatedPod = f

	/*-- start event handler --*/
	eh := events.NewEventHandler(events.Options{
		MaxWorkers:   5,
		MaxQueueSize: 20,
	})

	go eh.Listen(ctx, events.PodControl{
		UpdateStatus: PodHandler.UpdateStatusFromRuntime,
		LoadFromDisk: PodHandler.LoadPodFromKey,
		NotifyVirtualKubelet: func(pod *corev1.Pod) {
			if pod == nil {
				panic("this should not happen")
			}

			f(pod)

			v.Logger.Info(" * K8s status is synchronized",
				"version", pod.ResourceVersion,
				"phase", pod.Status.Phase,
			)
		},
	})

	/*-- add fileWatcher events to queue to be processed asynchronously --*/
	go func() {
		for {
			select {
			case event, ok := <-v.fileWatcher.Events():
				if !ok {
					v.Logger.Info("Failed to push event")
					return
				}
				eh.Push(event)

			case err, ok := <-v.fileWatcher.Errors():
				if !ok {
					v.Logger.Info("Failed to push error event")

					return
				}

				panic(errors.Wrapf(err, "fsnotify failed"))
			}
		}
	}()
}

func (v *VirtualK8S) PortForward(ctx context.Context, namespace, pod string, port int32, stream io.ReadWriteCloser) error {
	podKey := client.ObjectKey{Namespace: namespace, Name: pod}
	logger := v.Logger.WithValues("obj", podKey)

	logger.Info("[K8s] receive PortForward %q", pod)

	// in newer virtual-kubelet version we have support for port-forwarding
	return nil
}

/************************************************************

		Implements vkapi.VirtualK8S

************************************************************/

func (v *VirtualK8S) GetStatsSummary(context.Context) (*statsv1alpha1.Summary, error) {
	v.Logger.Info("[K8s] -> GetStatsSummary")
	defer v.Logger.Info("[K8s] <- GetStatsSummary")

	panic("not yet supported")
}

// GetContainerLogs retrieves the logs of a container by name from the provider.
func (v *VirtualK8S) GetContainerLogs(ctx context.Context, namespace, podName, containerName string, opts vkapi.ContainerLogOpts) (io.ReadCloser, error) {
	podKey := client.ObjectKey{Namespace: namespace, Name: podName}
	logger := v.Logger.WithValues("obj", podKey)

	logger.Info("[K8s] -> GetContainerLogs", "container", containerName)
	defer logger.Info("[K8s] <- GetContainerLogs", "container", containerName)

	logfilePath := compute.HPK.Pod(podKey).Container(containerName).LogsPath()

	/*---------------------------------------------------
	 * Log Streaming (With Follow)
	 *---------------------------------------------------*/
	if opts.Follow {
		// 	return io.NopCloser(strings.NewReader("Follow is not yet supported by HPK.\n")), nil
		v.Logger.Info("[K8s] WARNING -- Log with \"follow\" is not yet supported by HPK")

		/*
			seek := tail.SeekInfo{
				Offset: 0,
				Whence: 0,
			}

			// whence 0=origin, 2=end
			if opts.Tail >= 0 {
				seek.Whence = 2
			}

			tailConfig := tail.Config{
				MustExist: true,
				Poll:      true,
				Follow:    opts.Follow,
				Location:  &seek,
				DefaultLogger:    tail.DefaultLogger,
				ReOpen:    opts.Follow,
			}

			// tail the file and stream it to a pipe (that ends to a network socket).
			t, err := tail.TailFile(logfilePath, tailConfig)
			if err != nil {
				return nil, errors.Wrapf(err, "unable to stream log file")
			}

			pr, pw := io.Pipe()

			go func() {
				defer pw.Close()

				var line *tail.Line
				var ok bool

				for {
					select {
					case <-ctx.Done():
						// the consumer has cancelled
						t.Kill(errors.New("hangup by client"))
						return
					case line, ok = <-t.Lines:
						if !ok {
							// channel was closed
							return
						}
					}

					n, err := pw.Write([]byte(line.Text))
					if err != nil {
						panic("this should never happen:" + err.Error())
					}

					// Unfortunately this pience of code does not seem to work
					// because virtual kubelet does not support streaming.
					// Nonethess, it stays here for further investigation.
				}
			}()

			return io.NopCloser(pr), nil
		*/
	}

	/*---------------------------------------------------
	 * Log Batch (Without Follow)
	 *---------------------------------------------------*/
	if opts.Tail == 0 {
		// return everything
		logs, err := os.Open(logfilePath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				// return an empty response instead of an error
				return io.NopCloser(bytes.NewReader([]byte{})), nil
			}

			return nil, errors.Wrapf(err, "unable to batch logs")
		}

		return logs, nil
	}

	if opts.Tail > 0 {
		logs, err := container.GetTailLog(logfilePath, opts.Tail)
		if err != nil {
			return nil, errors.Wrapf(err, "unable to batch logs")
		}

		results := bytes.NewBuffer(nil)

		for _, nll := range logs {
			results.WriteString(nll + "\n")
		}

		return io.NopCloser(bytes.NewReader(results.Bytes())), nil
	}

	// return an empty response instead of an error
	return io.NopCloser(bytes.NewReader([]byte{})), nil
}

// RunInContainer executes a command in a container in the pod, copying data
// between in/out/err and the container's stdin/stdout/stderr.
func (v *VirtualK8S) RunInContainer(ctx context.Context, namespace, podName, containerName string, cmd []string, attach vkapi.AttachIO) error {
	podKey := client.ObjectKey{Namespace: namespace, Name: podName}
	logger := v.Logger.WithValues("obj", podKey)

	/*---------------------------------------------------
	 * Preamble used for Request tracing on the logs
	 *---------------------------------------------------*/
	logger.Info("[K8s] -> RunInContainer", "container", containerName)
	defer logger.Info("[K8s] <- RunInContainer", "container", containerName)

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
