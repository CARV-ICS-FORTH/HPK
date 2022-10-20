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
// limitations under the License.package main

package provider

import (
	"context"
	"io"
	"time"

	"github.com/carv-ics-forth/knoc/api"
	"github.com/carv-ics-forth/knoc/hpc"
	"github.com/carv-ics-forth/knoc/pkg/manager"
	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	vkapi "github.com/virtual-kubelet/virtual-kubelet/node/api"
	"github.com/virtual-kubelet/virtual-kubelet/node/api/statsv1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

// var _ vkprovider.InitFunc = (*NewProvider)(nil)

// Provider implements the virtual-kubelet provider interface and stores pods in memory.
type Provider struct {
	InitConfig

	Logger logr.Logger
}

// InitConfig is the config passed to initialize a registered provider.
type InitConfig struct {
	ConfigPath      string
	NodeName        string
	InternalIP      string
	DaemonPort      int32
	ResourceManager *manager.ResourceManager
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

// CreatePod accepts a Pod definition and stores it in memory.
func (p *Provider) CreatePod(ctx context.Context, pod *corev1.Pod) error {
	podKey := api.ObjectKeyFromObject(pod)
	logger := p.Logger.WithValues("obj", podKey)

	logger.Info("-> CreatePod")
	defer logger.Info("<- CreatePod")

	/*
		// set the ip of the running pods to the virtual node
		pod.Status.PodIP = provider.InternalIP
		pod.Status.PodIPs = []corev1.PodIP{{
			IP: provider.InternalIP,
		}}
	*/

	if err := hpc.CreatePod(ctx, pod, p.ResourceManager, api.DefaultContainerRegistry); err != nil {
		panic(errors.Wrapf(err, "failed to submit job"))
	}

	return nil
}

// UpdatePod accepts a Pod definition and updates its reference.
func (p *Provider) UpdatePod(_ context.Context, pod *corev1.Pod) error {
	p.Logger.Info("-> UpdatePod",
		"obj", api.ObjectKeyFromObject(pod),
		"phase", pod.Status.Phase,
	)

	defer p.Logger.Info("<- UpdatePod",
		"obj", api.ObjectKeyFromObject(pod),
		"phase", pod.Status.Phase,
	)

	/*

		podHandler, err := hpc.NewPodHandler(p.Logger, p.ResourceManager, pod, api.DefaultContainerRegistry)
		if err != nil {
			return errors.Wrap(err, "Could not create Pod from the underlying filesystem")
		}

		if err := podHandler.Save(); err != nil {
			return errors.Wrap(err, "Could not save Pod to the underlying filesystem")
		}

	*/

	return nil
}

// DeletePod deletes the specified pod out of memory.
func (p *Provider) DeletePod(ctx context.Context, pod *corev1.Pod) error {
	p.Logger.Info("-> DeletePod",
		"obj", api.ObjectKeyFromObject(pod),
	)

	defer p.Logger.Info("<- DeletePod",
		"obj", api.ObjectKeyFromObject(pod),
	)

	/*
		DeletePod takes a Kubernetes Pod and deletes it from the provider.
		TODO: Once a pod is deleted, the provider is expected to call the NotifyPods callback with a terminal pod status
		where all the containers are in a terminal state, as well as the pod.
		DeletePod may be called multiple times for the same pod
	*/
	if err := hpc.DeletePod(ctx, pod); err != nil {
		return errors.Wrapf(err, "failed to delete pods")
	}

	pod.Status.Phase = corev1.PodSucceeded

	for i := range pod.Status.InitContainerStatuses {
		pod.Status.InitContainerStatuses[i].State.Terminated = &corev1.ContainerStateTerminated{
			ExitCode:    0,
			Signal:      0,
			Reason:      "PodIsDeleted",
			Message:     "Pod is being deleted",
			StartedAt:   metav1.Time{Time: time.Now()},
			FinishedAt:  metav1.Time{Time: time.Now()},
			ContainerID: "",
		}
	}

	for i := range pod.Status.ContainerStatuses {
		pod.Status.ContainerStatuses[i].State.Terminated = &corev1.ContainerStateTerminated{
			ExitCode:    0,
			Signal:      0,
			Reason:      "PodIsDeleted",
			Message:     "Pod is being deleted",
			StartedAt:   metav1.Time{Time: time.Now()},
			FinishedAt:  metav1.Time{Time: time.Now()},
			ContainerID: "",
		}
	}

	return nil
}

// GetPod returns a pod by name that is stored in memory.
func (p *Provider) GetPod(ctx context.Context, namespace, name string) (*corev1.Pod, error) {
	p.Logger.Info("-> GetPod",
		"namespace", namespace,
		"name", name,
	)
	defer p.Logger.Info("<- GetPod",
		"namespace", namespace,
		"name", name,
	)

	podKey := api.ObjectKey{Namespace: namespace, Name: name}

	pod, err := hpc.LoadPod(ctx, podKey)
	if err != nil {
		return nil, errors.Wrapf(err, "unable to load pod '%s'", podKey)
	}

	return pod, nil
}

// GetPodStatus returns the status of a pod by name that is "running".
// returns nil if a pod by that name is not found.
func (p *Provider) GetPodStatus(ctx context.Context, namespace, name string) (*corev1.PodStatus, error) {
	p.Logger.Info("-> GetPodStatus",
		"namespace", namespace,
		"name", name,
	)

	defer p.Logger.Info("<- GetPodStatus",
		"namespace", namespace,
		"name", name,
	)

	podKey := api.ObjectKey{Namespace: namespace, Name: name}

	pod, err := hpc.LoadPod(ctx, podKey)
	if err != nil {
		return nil, errors.Wrapf(err, "unable to load pod '%s'", podKey)
	}

	return &pod.Status, nil
}

// GetPods returns a list of all pods known to be "running".
func (p *Provider) GetPods(_ context.Context) ([]*corev1.Pod, error) {
	p.Logger.Info("-> GetPods")
	defer p.Logger.Info("<- GetPods")

	/*
		TODO: inspect the underlying system for Pods in the Runtime Directory (.knoc)
		Pod status: inspect .knoc (network, volumes, lifetime)
		Container status: query SLURM
		Hint: use batch operations
	*/

	/*

		pods := make([]*corev1.Pod, 0)
		for _, cg := range p.GetCgs() {
			c := cg
			pod, err := containerGroupToPod(&c)
			if err != nil {
				msg := fmt.Sprint("error converting container group to pod", cg.ContainerGroupId, err)
				log.G(context.TODO()).WithField("Method", "GetPods").Info(msg)
				continue
			}
			pods = append(pods, pod)
		}
		return pods, nil

		var pods []*corev1.Pod

		// TODO: we need "running" pods, or everything ?

		for _, pod := range p.pods {
			pods = append(pods, pod)
		}

	*/

	return nil, nil
}

/************************************************************

		Implements vkapi.Provider

************************************************************/

// GetContainerLogs retrieves the logs of a container by name from the provider.
func (p *Provider) GetContainerLogs(_ context.Context, namespace, podName, containerName string, _ vkapi.ContainerLogOpts) (io.ReadCloser, error) {
	p.Logger.Info("-> GetContainerLogs")
	defer p.Logger.Info("<- GetContainerLogs")

	key := api.ObjectKey{
		Namespace: namespace,
		Name:      podName,
	}

	/*
		stdout, err := os.OpenFile(paths.StdOutputFilePath(key, containerName))
		if err != nil {
			return nil, errors.Wrapf(err, "Failed to open stdout")
		}

		stderr, err := os.OpenFile(paths.StdErrorFilePath(key, containerName))
		if err != nil {
			return nil, errors.Wrapf(err, "Failed to open stdout")
		}

		io.MultiReader()

		key := api.ObjectKey{
			Namespace: namespace,
			Name:      podName,
		}


	*/
	_ = key
	/*
		// if _, exists := p.pods[key]; !exists {
			return nil, errors.Errorf("pod '%s' is not known to the provider", key)
		}

		// search in pod running directory, for the container.out
		containerLogsFile := filepath.Join(api.RuntimeDir, podName, containerName, ".out")

		f, err := os.Open(containerLogsFile)
		if err != nil {
			return nil, errors.Wrapf(err, "cannot open file '%s'", containerLogsFile)
		}

		return f, nil

	*/
	return nil, errors.Errorf("ContainerLogs is not yet supported")
}

// RunInContainer executes a command in a container in the pod, copying data
// between in/out/err and the container's stdin/stdout/stderr.
func (p *Provider) RunInContainer(_ context.Context, namespace, name, container string, cmd []string, attach vkapi.AttachIO) error {
	p.Logger.Info("-> RunInContainer")
	defer p.Logger.Info("<- RunInContainer")

	p.Logger.Info("RunInContainer not supported", "cmd", cmd)

	return nil
}

func (p *Provider) ConfigureNode(_ context.Context, _ corev1.Node) {
	p.Logger.Info("-> ConfigureNode")
	defer p.Logger.Info("<- ConfigureNode")
}

func (p *Provider) GetStatsSummary(context.Context) (*statsv1alpha1.Summary, error) {
	p.Logger.Info("-> GetStatsSummary")
	defer p.Logger.Info("<- GetStatsSummary")

	panic("poutsakia")
}
