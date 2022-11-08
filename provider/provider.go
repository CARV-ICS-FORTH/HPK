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

	"github.com/carv-ics-forth/hpk/compute/slurm"
	"github.com/carv-ics-forth/hpk/pkg/resourcemanager"
	"github.com/go-logr/logr"
	"github.com/niemeyer/pretty"
	"github.com/pkg/errors"
	vkapi "github.com/virtual-kubelet/virtual-kubelet/node/api"
	"github.com/virtual-kubelet/virtual-kubelet/node/api/statsv1alpha1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

// Provider implements the virtual-kubelet provider interface and stores pods in memory.
type Provider struct {
	InitConfig

	Logger logr.Logger
}

// InitConfig is the config passed to initialize a registered provider.
type InitConfig struct {
	NodeName        string
	InternalIP      string
	DaemonPort      int32
	ResourceManager *resourcemanager.ResourceManager

	BuildVersion string
}

// NewProvider creates a new Provider, which implements the PodNotifier interface
func NewProvider(config InitConfig) (*Provider, error) {
	return &Provider{
		InitConfig: config,
		Logger:     zap.New(zap.UseDevMode(true)),
	}, nil
}

/************************************************************

		Implements node.PodLifecycleHandler

************************************************************/

// CreatePod accepts a VirtualEnvironment definition and stores it in memory.
func (p *Provider) CreatePod(ctx context.Context, pod *corev1.Pod) error {
	podKey := client.ObjectKeyFromObject(pod)
	logger := p.Logger.WithValues("obj", podKey)

	/*---------------------------------------------------
	 * Preamble used for Request tracing on the logs
	 *---------------------------------------------------*/
	logger.Info("-> CreatePod")

	defer func() {
		logger.Info("<- CreatePod", "podIP", pod.Status.PodIP)
	}()

	/*---------------------------------------------------
	 * Match the IPs between host and pod.
	 *---------------------------------------------------*/

	/*
		This is needed so that node-level logging requests
		will be handled by the Virtual Kubelet
		https://ritazh.com/understanding-kubectl-logs-with-virtual-kubelet-a135e83ae0ee
	*/
	pod.Status.HostIP = p.InitConfig.InternalIP

	/*---------------------------------------------------
	 * Pass the pod for creation to the backend
	 *---------------------------------------------------*/
	if err := slurm.CreatePod(ctx, pod, p.ResourceManager); err != nil {
		return errors.Wrapf(err, "unable to create pod")
	}

	return nil
}

// UpdatePod accepts a VirtualEnvironment definition and updates its reference.
func (p *Provider) UpdatePod(ctx context.Context, newPod *corev1.Pod) error {
	podKey := client.ObjectKeyFromObject(newPod)
	logger := p.Logger.WithValues("obj", podKey)

	/*---------------------------------------------------
	 * Preamble used for Request tracing on the logs
	 *---------------------------------------------------*/
	logger.Info("-> UpdatePod", "phase", newPod.Status.Phase)

	defer func() {
		logger.Info("<- UpdatePod", "phase", newPod.Status.Phase)
	}()

	/*---------------------------------------------------
	 * Ensure that received pod is newer than the local
	 *---------------------------------------------------*/
	oldPod, err := slurm.GetPod(podKey)
	if err != nil {
		return errors.Wrapf(err, "unable to load local pod")
	}

	if oldPod.ResourceVersion >= newPod.ResourceVersion {
		/*-- The received pod is old, so we can safely discard it --*/
		logger.Info("Discard update since its ResourceVersion is older than the local")

		return nil
	}

	/*---------------------------------------------------
	 * Identify any intermediate actions that must taken
	 *---------------------------------------------------*/
	if metaDiff := pretty.Diff(oldPod.ObjectMeta, newPod.ObjectMeta); len(metaDiff) > 0 {
		/* ... */
	}

	if specDiff := pretty.Diff(oldPod.Spec, newPod.Spec); len(specDiff) > 0 {
		/* ... */
	}

	if statusDiff := pretty.Diff(oldPod.Status, newPod.Status); len(statusDiff) > 0 {
		/* ... */
	}

	/*
		WARNING: We should never replace the old version with the newer
		since it may lead to race conditions.

		It is possible to Slurm to trigger a local event, that will
		be lost if we replace the version.
	*/

	return nil
}

// DeletePod deletes the specified pod out of memory.
func (p *Provider) DeletePod(ctx context.Context, pod *corev1.Pod) error {
	podKey := client.ObjectKeyFromObject(pod)
	logger := p.Logger.WithValues("obj", podKey)

	/*---------------------------------------------------
	 * Preamble used for Request tracing on the logs
	 *---------------------------------------------------*/
	logger.Info("-> DeletePod")
	defer logger.Info("<- DeletePod")

	return slurm.DeletePod(pod)
}

// GetPods returns a list of all pods known to be "running".
func (p *Provider) GetPods(ctx context.Context) ([]*corev1.Pod, error) {
	/*---------------------------------------------------
	 * Preamble used for Request tracing on the logs
	 *---------------------------------------------------*/
	p.Logger.Info("-> GetPods")
	defer p.Logger.Info("<- GetPods")

	return slurm.GetPods()
}

// GetPod returns a pod by name that is stored in memory.
func (p *Provider) GetPod(ctx context.Context, namespace, name string) (*corev1.Pod, error) {
	podKey := client.ObjectKey{Namespace: namespace, Name: name}
	logger := p.Logger.WithValues("obj", podKey)

	/*---------------------------------------------------
	 * Preamble used for Request tracing on the logs
	 *---------------------------------------------------*/
	logger.Info("-> GetPod")
	defer logger.Info("<- GetPod")

	return slurm.GetPod(podKey)
}

// GetPodStatus returns the status of a pod by name that is "running".
// returns nil if a pod by that name is not found.
func (p *Provider) GetPodStatus(ctx context.Context, namespace, name string) (*corev1.PodStatus, error) {
	podKey := client.ObjectKey{Namespace: namespace, Name: name}
	logger := p.Logger.WithValues("obj", podKey)

	/*---------------------------------------------------
	 * Preamble used for Request tracing on the logs
	 *---------------------------------------------------*/
	logger.Info("-> GetPodStatus")

	pod, err := slurm.GetPod(podKey)
	if err != nil {
		/*
			if the pod is not found, then the only thing we can do is to create a mock-up status.
			on error, the virtual-kubelet sets the status.Reason to "ProviderFailure" and the status.Message to err.
			however, it is up to us to set the status.Phase to failed.
		*/
		return &corev1.PodStatus{Phase: corev1.PodFailed}, errors.Wrapf(err, "unable to load pod '%s'", podKey)
	}

	logger.Info("<- GetPodStatus", "phase", pod.Status.Phase)

	return &pod.Status, nil
}

/************************************************************

		Implements vkapi.Provider

************************************************************/

// GetContainerLogs retrieves the logs of a container by name from the provider.
func (p *Provider) GetContainerLogs(ctx context.Context, namespace, podName, containerName string, opts vkapi.ContainerLogOpts) (io.ReadCloser, error) {
	podKey := client.ObjectKey{Namespace: namespace, Name: podName}
	logger := p.Logger.WithValues("obj", podKey)

	/*---------------------------------------------------
	 * Preamble used for Request tracing on the logs
	 *---------------------------------------------------*/
	logger.Info("-> GetContainerLogs", "container", containerName)
	defer logger.Info("<- GetContainerLogs", "container", containerName)

	panic("not yet supported")
}

// RunInContainer executes a command in a container in the pod, copying data
// between in/out/err and the container's stdin/stdout/stderr.
func (p *Provider) RunInContainer(ctx context.Context, namespace, podName, containerName string, cmd []string, attach vkapi.AttachIO) error {
	podKey := client.ObjectKey{Namespace: namespace, Name: podName}
	logger := p.Logger.WithValues("obj", podKey)

	/*---------------------------------------------------
	 * Preamble used for Request tracing on the logs
	 *---------------------------------------------------*/
	logger.Info("-> RunInContainer", "container", containerName)
	defer logger.Info("<- RunInContainer", "container", containerName)

	panic("not yet supported")
}

func (p *Provider) ConfigureNode(_ context.Context, _ corev1.Node) {
	/*---------------------------------------------------
	 * Preamble used for Request tracing on the logs
	 *---------------------------------------------------*/
	p.Logger.Info("-> ConfigureNode")
	defer p.Logger.Info("<- ConfigureNode")

	panic("not yet supported")
}

func (p *Provider) GetStatsSummary(context.Context) (*statsv1alpha1.Summary, error) {
	/*---------------------------------------------------
	 * Preamble used for Request tracing on the logs
	 *---------------------------------------------------*/
	p.Logger.Info("-> GetStatsSummary")
	defer p.Logger.Info("<- GetStatsSummary")

	panic("not yet supported")
}
