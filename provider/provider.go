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
	"os"
	"path/filepath"
	"runtime"

	"github.com/carv-ics-forth/knoc/api"
	"github.com/carv-ics-forth/knoc/pkg/manager"
	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	vkapi "github.com/virtual-kubelet/virtual-kubelet/node/api"
	"github.com/virtual-kubelet/virtual-kubelet/node/api/statsv1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

// var _ vkprovider.InitFunc = (*NewProvider)(nil)

// Provider implements the virtual-kubelet provider interface and stores pods in memory.
type Provider struct {
	InitConfig

	pods map[client.ObjectKey]*corev1.Pod
	// startTime time.Time
	// notifier  func(*corev1.Pod)

	logger logr.Logger

	resources corev1.ResourceList
}

// InitConfig is the config passed to initialize a registered provider.
type InitConfig struct {
	ConfigPath string
	NodeName   string
	InternalIP string
	DaemonPort int32

	ResourceManager *manager.ResourceManager
}

// NewProvider creates a new Provider, which implements the PodNotifier interface
func NewProvider(config InitConfig) (*Provider, error) {
	return &Provider{
		InitConfig: config,
		pods:       make(map[client.ObjectKey]*corev1.Pod),
		// startTime:  time.Now(),
		// notifier:   nil,
		logger: zap.New(zap.UseDevMode(true)),
		resources: corev1.ResourceList{
			"cpu":    resource.MustParse("30"),
			"memory": resource.MustParse("10Gi"),
			"pods":   resource.MustParse("100"),
		},
	}, nil
}

/************************************************************

		Implements node.PodLifecycleHandler

************************************************************/

// CreatePod accepts a Pod definition and stores it in memory.
func (p *Provider) CreatePod(ctx context.Context, pod *corev1.Pod) error {
	p.logger.Info("-> CreatePod",
		"obj", client.ObjectKeyFromObject(pod),
	)

	defer p.logger.Info("<- CreatePod",
		"obj", client.ObjectKeyFromObject(pod),
	)

	// Submit job for remote execution
	/*
		if err := RemoteExecution(p, ctx, api.SUBMIT, pod); err != nil {
		 	return errors.Wrapf(err, "Failed to Delete pod '%s'", pod.GetName())
		}
	*/
	key := client.ObjectKeyFromObject(pod)
	p.pods[key] = pod
	// p.notifier(pod)

	return nil

}

// UpdatePod accepts a Pod definition and updates its reference.
func (p *Provider) UpdatePod(_ context.Context, pod *corev1.Pod) error {
	p.logger.Info("-> UpdatePod",
		"obj", client.ObjectKeyFromObject(pod),
	)

	defer p.logger.Info("<- UpdatePod",
		"obj", client.ObjectKeyFromObject(pod),
	)

	key := client.ObjectKeyFromObject(pod)

	p.pods[key] = pod
	// p.notifier(pod)

	return nil
}

// DeletePod deletes the specified pod out of memory.
func (p *Provider) DeletePod(ctx context.Context, pod *corev1.Pod) error {
	p.logger.Info("-> DeletePod",
		"obj", client.ObjectKeyFromObject(pod),
	)

	defer p.logger.Info("<- DeletePod",
		"obj", client.ObjectKeyFromObject(pod),
	)

	key := client.ObjectKeyFromObject(pod)

	// check if the pod is managed
	if _, exists := p.pods[key]; !exists {
		return errors.Errorf("key '%s' not found", key)
	}

	if err := RemoteExecution(p, ctx, api.DELETE, pod); err != nil {
		return errors.Wrapf(err, "Failed to Delete pod '%s'", pod.GetName())
	}

	// p.notifier(pod)
	delete(p.pods, key)

	return nil
}

// GetPod returns a pod by name that is stored in memory.
func (p *Provider) GetPod(ctx context.Context, namespace, name string) (*corev1.Pod, error) {
	p.logger.Info("-> GetPod",
		"namespace", namespace,
		"name", name,
	)
	defer p.logger.Info("<- GetPod",
		"namespace", namespace,
		"name", name,
	)

	pods, err := p.GetPods(ctx)
	if err != nil {
		return nil, err
	}
	for _, pod := range pods {
		if pod.Name == name && pod.Namespace == namespace {
			return pod, nil
		}
	}

	return nil, nil
}

// GetPodStatus returns the status of a pod by name that is "running".
// returns nil if a pod by that name is not found.
func (p *Provider) GetPodStatus(ctx context.Context, namespace, name string) (*corev1.PodStatus, error) {
	p.logger.Info("-> GetPodStatus",
		"namespace", namespace,
		"name", name,
	)
	defer p.logger.Info("<- GetPodStatus",
		"namespace", namespace,
		"name", name,
	)

	pod, err := p.GetPod(ctx, namespace, name)
	if err != nil {
		return nil, errors.Wrapf(err, "cannot get pod")
	}

	if pod == nil {
		return nil, nil
	}

	return &pod.Status, nil
}

// GetPods returns a list of all pods known to be "running".
func (p *Provider) GetPods(_ context.Context) ([]*corev1.Pod, error) {
	p.logger.Info("-> GetPods")
	defer p.logger.Info("<- GetPods")

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
	p.logger.Info("-> GetContainerLogs")
	defer p.logger.Info("<- GetContainerLogs")

	key := client.ObjectKey{
		Namespace: namespace,
		Name:      podName,
	}

	if _, exists := p.pods[key]; !exists {
		return nil, errors.Errorf("pod '%s' is not known to the provider", key)
	}

	// search in pod running directory, for the container.out
	containerLogsFile := filepath.Join(api.RuntimeDir, podName, containerName, ".out")

	f, err := os.Open(containerLogsFile)
	if err != nil {
		return nil, errors.Wrapf(err, "cannot open file '%s'", containerLogsFile)
	}

	return f, nil
}

// RunInContainer executes a command in a container in the pod, copying data
// between in/out/err and the container's stdin/stdout/stderr.
func (p *Provider) RunInContainer(_ context.Context, namespace, name, container string, cmd []string, attach vkapi.AttachIO) error {
	p.logger.Info("-> RunInContainer")
	defer p.logger.Info("<- RunInContainer")

	p.logger.Info("RunInContainer not supported", "cmd", cmd)

	return nil
}

func (p *Provider) ConfigureNode(_ context.Context, n *corev1.Node) {
	p.logger.Info("-> ConfigureNode")
	defer p.logger.Info("<- ConfigureNode")

	internalIP := "127.0.0.1"

	n.Status.Capacity = p.resources
	n.Status.Allocatable = p.resources
	n.Status.Conditions = p.nodeConditions()
	n.Status.Addresses = []corev1.NodeAddress{{
		Type:    "InternalIP",
		Address: internalIP,
	}}

	n.Status.DaemonEndpoints = corev1.NodeDaemonEndpoints{
		KubeletEndpoint: corev1.DaemonEndpoint{
			Port: p.InitConfig.DaemonPort,
		},
	}

	n.Status.NodeInfo.OperatingSystem = runtime.GOOS
	n.Status.NodeInfo.Architecture = "amd64"
	n.ObjectMeta.Labels["alpha.service-controller.kubernetes.io/exclude-balancer"] = "true"
	n.ObjectMeta.Labels["node.kubernetes.io/exclude-from-external-load-balancers"] = "true"
}

// NodeConditions returns a list of conditions (Ready, OutOfDisk, etc), for updates to the node status
// within Kubernetes.
func (p *Provider) nodeConditions() []corev1.NodeCondition {
	p.logger.Info("-> nodeConditions")
	defer p.logger.Info("<- nodeConditions")

	// TODO: Make this configurable
	return []corev1.NodeCondition{
		{
			Type:               corev1.NodeReady,
			Status:             corev1.ConditionTrue,
			LastHeartbeatTime:  metav1.Now(),
			LastTransitionTime: metav1.Now(),
			Reason:             "KubeletPending",
			Message:            "kubelet is pending.",
		},
		{
			Type:               corev1.NodeMemoryPressure,
			Status:             corev1.ConditionFalse,
			LastHeartbeatTime:  metav1.Now(),
			LastTransitionTime: metav1.Now(),
			Reason:             "KubeletHasSufficientMemory",
			Message:            "kubelet has sufficient memory available",
		},
		{
			Type:               corev1.NodeDiskPressure,
			Status:             corev1.ConditionFalse,
			LastHeartbeatTime:  metav1.Now(),
			LastTransitionTime: metav1.Now(),
			Reason:             "KubeletHasNoDiskPressure",
			Message:            "kubelet has no disk pressure",
		},
		{
			Type:               corev1.NodePIDPressure,
			Status:             corev1.ConditionFalse,
			LastHeartbeatTime:  metav1.Now(),
			LastTransitionTime: metav1.Now(),
			Reason:             "KubeletHasNoPIDPressure",
			Message:            "kubelet has no PID pressure",
		},
		{
			Type:               corev1.NodeNetworkUnavailable,
			Status:             corev1.ConditionFalse,
			LastHeartbeatTime:  metav1.Now(),
			LastTransitionTime: metav1.Now(),
			Reason:             "RouteCreated",
			Message:            "RouteController created a route",
		},
	}
}

func (p *Provider) GetStatsSummary(context.Context) (*statsv1alpha1.Summary, error) {
	p.logger.Info("-> GetStatsSummary")
	defer p.logger.Info("<- GetStatsSummary")

	panic("poutsakia")
}

/************************************************************

		Needed to avoid dependencies on nodeutils.

************************************************************/

func (p *Provider) Capacity(ctx context.Context) corev1.ResourceList {
	p.logger.Info("-> Capacity")
	defer p.logger.Info("<- Capacity ", "resources", p.resources)

	return p.resources
}

func (p *Provider) NodeConditions(ctx context.Context) []corev1.NodeCondition {
	p.logger.Info("-> NodeConditions")
	defer p.logger.Info("<- NodeConditions")

	return p.nodeConditions()
}

func (p *Provider) NodeAddresses(ctx context.Context) []corev1.NodeAddress {
	p.logger.Info("-> NodeAddresses")
	defer p.logger.Info("<- NodeAddresses")

	return nil
}

func (p *Provider) NodeDaemonEndpoints(ctx context.Context) *corev1.NodeDaemonEndpoints {
	p.logger.Info("-> NodeDaemonEndpoints")
	defer p.logger.Info("<- NodeDaemonEndpoints")

	return &corev1.NodeDaemonEndpoints{}
}

/*
// NotifyPods is called to set a pod notifier callback function. This should be called before any operations are done
// within the provider.
func (p *Provider) NotifyPods(ctx context.Context, f func(*corev1.Pod)) {
	p.notifier = f
	go p.statusLoop(ctx)
}

func (p *Provider) statusLoop(ctx context.Context) {
	t := time.NewTimer(5 * time.Second)
	if !t.Stop() {
		<-t.C
	}

	for {
		t.Reset(5 * time.Second)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}

		checkPodsStatus(p, ctx)
	}
}

*/
