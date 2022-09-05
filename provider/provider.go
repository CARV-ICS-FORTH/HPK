// Copyright © 2021 FORTH-ICS
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
	"encoding/json"
	"io"
	"io/ioutil"
	"os"
	"strings"
	"time"

	"github.com/carv-ics-forth/knoc/api"
	"github.com/pkg/errors"
	"github.com/sfreiberg/simplessh"
	"github.com/virtual-kubelet/node-cli/manager"
	"github.com/virtual-kubelet/virtual-kubelet/errdefs"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	vkapi "github.com/virtual-kubelet/virtual-kubelet/node/api"
	"github.com/virtual-kubelet/virtual-kubelet/trace"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Provider implements the virtual-kubelet provider interface and stores pods in memory.
type Provider struct {
	nodeName           string
	operatingSystem    string
	internalIP         string
	daemonEndpointPort int32
	pods               map[string]*corev1.Pod
	config             Config
	startTime          time.Time
	resourceManager    *manager.ResourceManager
	notifier           func(*corev1.Pod)
}

type Config struct {
	CPU    string `json:"cpu,omitempty"`
	Memory string `json:"memory,omitempty"`
	Pods   string `json:"pods,omitempty"`
}

// NewProviderConfig creates a new KNOCV0Provider. KNOC legacy provider does not implement the new asynchronous podnotifier interface
func NewProviderConfig(config Config, nodeName, operatingSystem string, internalIP string, rm *manager.ResourceManager, daemonEndpointPort int32) (*Provider, error) {
	// set defaults
	if config.CPU == "" {
		config.CPU = api.DefaultCPUCapacity
	}

	if config.Memory == "" {
		config.Memory = api.DefaultMemoryCapacity
	}

	if config.Pods == "" {
		config.Pods = api.DefaultPodCapacity
	}

	provider := Provider{
		nodeName:           nodeName,
		operatingSystem:    operatingSystem,
		internalIP:         internalIP,
		daemonEndpointPort: daemonEndpointPort,
		resourceManager:    rm,
		pods:               make(map[string]*corev1.Pod),
		config:             config,
		startTime:          time.Now(),
	}

	return &provider, nil
}

// NewProvider creates a new Provider, which implements the PodNotifier interface
func NewProvider(providerConfig, nodeName, operatingSystem string, internalIP string, rm *manager.ResourceManager, daemonEndpointPort int32) (*Provider, error) {
	config, err := loadConfig(providerConfig, nodeName)
	if err != nil {
		return nil, err
	}
	return NewProviderConfig(config, nodeName, operatingSystem, internalIP, rm, daemonEndpointPort)
}

// loadConfig loads the given json configuration files.
func loadConfig(providerConfig, nodeName string) (config Config, err error) {
	data, err := os.ReadFile(providerConfig)
	if err != nil {
		return config, errors.Wrapf(err, "cannot read file '%s'", providerConfig)
	}

	configMap := map[string]Config{}

	if err := json.Unmarshal(data, &configMap); err != nil {
		return config, errors.Wrapf(err, "cannot unmarshal")
	}

	if _, exist := configMap[nodeName]; exist {
		config = configMap[nodeName]
		if config.CPU == "" {
			config.CPU = api.DefaultCPUCapacity
		}
		if config.Memory == "" {
			config.Memory = api.DefaultMemoryCapacity
		}
		if config.Pods == "" {
			config.Pods = api.DefaultPodCapacity
		}
	}

	if _, err := resource.ParseQuantity(config.CPU); err != nil {
		return config, errors.Wrapf(err, "Invalid CPU Value")
	}
	if _, err := resource.ParseQuantity(config.Memory); err != nil {
		return config, errors.Wrapf(err, "Invalid Memory Value")
	}
	if _, err := resource.ParseQuantity(config.Pods); err != nil {
		return config, errors.Wrapf(err, "Invalid pods value %v", config.Pods)
	}
	return config, nil
}

// CreatePod accepts a Pod definition and stores it in memory.
func (p *Provider) CreatePod(ctx context.Context, pod *corev1.Pod) error {
	var hasInitContainers = false
	var state corev1.ContainerState

	// Add the pod's coordinates to the current span.
	key, err := api.BuildKey(pod)
	if err != nil {
		return err
	}

	if err := RemoteExecution(p, ctx, api.SUBMIT, pod); err != nil {
		return errors.Wrapf(err, "Failed to Delete pod '%s'", pod.GetName())
	}

	now := metav1.NewTime(time.Now())

	runningState := corev1.ContainerState{
		Running: &corev1.ContainerStateRunning{
			StartedAt: now,
		},
	}

	waitingState := corev1.ContainerState{
		Waiting: &corev1.ContainerStateWaiting{
			Reason: "Waiting for InitContainers",
		},
	}
	state = runningState

	p.pods[key] = pod
	p.notifier(pod)

	return nil
}

// UpdatePod accepts a Pod definition and updates its reference.
func (p *Provider) UpdatePod(ctx context.Context, pod *corev1.Pod) error {
	ctx, span := trace.StartSpan(ctx, "UpdatePod")
	defer span.End()

	// Add the pod's coordinates to the current span.
	ctx = addAttributes(ctx, span, api.NamespaceKey, pod.Namespace, api.NameKey, pod.Name)

	log.G(ctx).Infof("receive UpdatePod %q", pod.Name)

	key, err := api.BuildKey(pod)
	if err != nil {
		return err
	}

	p.pods[key] = pod
	p.notifier(pod)

	return nil
}

// DeletePod deletes the specified pod out of memory.
func (p *Provider) DeletePod(ctx context.Context, pod *corev1.Pod) (err error) {
	ctx, span := trace.StartSpan(ctx, "DeletePod")
	defer span.End()

	// Add the pod's coordinates to the current span.
	ctx = addAttributes(ctx, span, api.NamespaceKey, pod.Namespace, api.NameKey, pod.Name)

	log.G(ctx).Infof("receive DeletePod %q", pod.Name)

	key, err := api.BuildKey(pod)
	if err != nil {
		return err
	}

	if _, exists := p.pods[key]; !exists {
		return errdefs.NotFound("pod not found")
	}

	now := metav1.Now()
	pod.Status.Phase = corev1.PodSucceeded
	pod.Status.Reason = "KNOCProviderPodDeleted"

	if err := RemoteExecution(p, ctx, api.DELETE, pod); err != nil {
		return errors.Wrapf(err, "Failed to Delete pod '%s'", pod.GetName())
	}

	for i := range pod.Status.ContainerStatuses {
		pod.Status.ContainerStatuses[i].Ready = false
		pod.Status.ContainerStatuses[i].State = corev1.ContainerState{
			Terminated: &corev1.ContainerStateTerminated{
				Message:    "KNOC provider terminated container upon deletion",
				FinishedAt: now,
				Reason:     "KNOCProviderPodContainerDeleted",
				// StartedAt:  pod.Status.ContainerStatuses[i].State.Running.StartedAt,
			},
		}
	}

	for idx := range pod.Status.InitContainerStatuses {
		pod.Status.InitContainerStatuses[idx].Ready = false
		pod.Status.InitContainerStatuses[idx].State = corev1.ContainerState{
			Terminated: &corev1.ContainerStateTerminated{
				Message:    "KNOC provider terminated container upon deletion",
				FinishedAt: now,
				Reason:     "KNOCProviderPodContainerDeleted",
				// StartedAt:  pod.Status.InitContainerStatuses[i].State.Running.StartedAt,
			},
		}
	}

	p.notifier(pod)
	delete(p.pods, key)

	return nil
}

// GetPod returns a pod by name that is stored in memory.
func (p *Provider) GetPod(ctx context.Context, namespace, name string) (pod *corev1.Pod, err error) {
	ctx, span := trace.StartSpan(ctx, "GetPod")
	defer func() {
		span.SetStatus(err)
		span.End()
	}()

	// Add the pod's coordinates to the current span.
	ctx = addAttributes(ctx, span, api.NamespaceKey, namespace, api.NameKey, name)

	log.G(ctx).Infof("receive GetPod %q", name)

	key, err := api.BuildKeyFromNames(namespace, name)
	if err != nil {
		return nil, err
	}

	if pod, ok := p.pods[key]; ok {
		return pod, nil
	}
	return nil, errdefs.NotFoundf("pod \"%s/%s\" is not known to the provider", namespace, name)
}

// GetContainerLogs retrieves the logs of a container by name from the provider.
func (p *Provider) GetContainerLogs(ctx context.Context, namespace, podName, containerName string, _ vkapi.ContainerLogOpts) (io.ReadCloser, error) {
	ctx, span := trace.StartSpan(ctx, "GetContainerLogs")
	defer span.End()

	// Add pod and container attributes to the current span.
	ctx = addAttributes(ctx, span, api.NamespaceKey, namespace, api.NameKey, podName, api.ContainerNameKey, containerName)

	log.G(ctx).Infof("receive GetContainerLogs %q", podName)
	client, err := simplessh.ConnectWithKey(os.Getenv("REMOTE_HOST")+":"+os.Getenv("REMOTE_PORT"), os.Getenv("REMOTE_USER"), os.Getenv("REMOTE_KEY"))
	if err != nil {
		panic(err)
	}
	defer client.Close()
	key, err := api.BuildKeyFromNames(namespace, podName)
	if err != nil {
		return nil, err
	}

	pod := p.pods[key]
	instanceName := ""

	for i := range pod.Spec.InitContainers {
		if pod.Spec.InitContainers[i].Name == containerName {
			instanceName = BuildRemoteExecutionInstanceName(&pod.Spec.InitContainers[i], pod)
		}
	}

	for i := range pod.Spec.Containers {
		if pod.Spec.Containers[i].Name == containerName {
			instanceName = BuildRemoteExecutionInstanceName(&pod.Spec.Containers[i], pod)
		}
	}
	// in case we dont find it or if it hasnt run yet we should return empty string
	output, _ := client.Exec("cat " + ".knoc/" + instanceName + ".out ")

	return ioutil.NopCloser(strings.NewReader(string(output))), nil
}

// RunInContainer executes a command in a container in the pod, copying data
// between in/out/err and the container's stdin/stdout/stderr.
func (p *Provider) RunInContainer(ctx context.Context, namespace, name, container string, cmd []string, attach vkapi.AttachIO) error {
	client, err := simplessh.ConnectWithKey(os.Getenv("REMOTE_HOST")+":"+os.Getenv("REMOTE_PORT"), os.Getenv("REMOTE_USER"), os.Getenv("REMOTE_KEY"))
	if err != nil {
		return errors.Wrapf(err, "connect with key")
	}

	defer client.Close()

	if _, err := client.Exec(strings.Join(cmd, " ")); err != nil {
		return errors.Wrapf(err, "run in container")
	}

	return nil
}

// GetPodStatus returns the status of a pod by name that is "running".
// returns nil if a pod by that name is not found.
func (p *Provider) GetPodStatus(ctx context.Context, namespace, name string) (*corev1.PodStatus, error) {
	ctx, span := trace.StartSpan(ctx, "GetPodStatus")
	defer span.End()

	// Add namespace and name as attributes to the current span.
	ctx = addAttributes(ctx, span, api.NamespaceKey, namespace, api.NameKey, name)

	log.G(ctx).Infof("receive GetPodStatus %q", name)

	pod, err := p.GetPod(ctx, namespace, name)
	if err != nil {
		return nil, err
	}

	return &pod.Status, nil
}

// GetPods returns a list of all pods known to be "running".
func (p *Provider) GetPods(ctx context.Context) ([]*corev1.Pod, error) {
	ctx, span := trace.StartSpan(ctx, "GetPods")
	defer span.End()

	log.G(ctx).Info("receive GetPods")

	var pods []*corev1.Pod

	for _, pod := range p.pods {
		pods = append(pods, pod)
	}

	return pods, nil
}

func (p *Provider) ConfigureNode(ctx context.Context, n *corev1.Node) { // nolint:golint
	ctx, span := trace.StartSpan(ctx, "KNOC.ConfigureNode") // nolint:staticcheck,ineffassign
	defer span.End()

	n.Status.Capacity = p.capacity()
	n.Status.Allocatable = p.capacity()
	n.Status.Conditions = p.nodeConditions()
	n.Status.Addresses = p.nodeAddresses()
	n.Status.DaemonEndpoints = p.nodeDaemonEndpoints()

	os := p.operatingSystem
	if os == "" {
		os = "Linux"
	}
	n.Status.NodeInfo.OperatingSystem = os
	n.Status.NodeInfo.Architecture = "amd64"
	n.ObjectMeta.Labels["alpha.service-controller.kubernetes.io/exclude-balancer"] = "true"
	n.ObjectMeta.Labels["node.kubernetes.io/exclude-from-external-load-balancers"] = "true"
}

// Capacity returns a resource list containing the capacity limits.
func (p *Provider) capacity() corev1.ResourceList {
	return corev1.ResourceList{
		"cpu":    resource.MustParse(p.config.CPU),
		"memory": resource.MustParse(p.config.Memory),
		"pods":   resource.MustParse(p.config.Pods),
	}
}

// NodeConditions returns a list of conditions (Ready, OutOfDisk, etc), for updates to the node status
// within Kubernetes.
func (p *Provider) nodeConditions() []corev1.NodeCondition {
	// TODO: Make this configurable
	return []corev1.NodeCondition{
		{
			Type:               "Ready",
			Status:             corev1.ConditionTrue,
			LastHeartbeatTime:  metav1.Now(),
			LastTransitionTime: metav1.Now(),
			Reason:             "KubeletPending",
			Message:            "kubelet is pending.",
		},
		{
			Type:               "OutOfDisk",
			Status:             corev1.ConditionFalse,
			LastHeartbeatTime:  metav1.Now(),
			LastTransitionTime: metav1.Now(),
			Reason:             "KubeletHasSufficientDisk",
			Message:            "kubelet has sufficient disk space available",
		},
		{
			Type:               "MemoryPressure",
			Status:             corev1.ConditionFalse,
			LastHeartbeatTime:  metav1.Now(),
			LastTransitionTime: metav1.Now(),
			Reason:             "KubeletHasSufficientMemory",
			Message:            "kubelet has sufficient memory available",
		},
		{
			Type:               "DiskPressure",
			Status:             corev1.ConditionFalse,
			LastHeartbeatTime:  metav1.Now(),
			LastTransitionTime: metav1.Now(),
			Reason:             "KubeletHasNoDiskPressure",
			Message:            "kubelet has no disk pressure",
		},
		{
			Type:               "NetworkUnavailable",
			Status:             corev1.ConditionFalse,
			LastHeartbeatTime:  metav1.Now(),
			LastTransitionTime: metav1.Now(),
			Reason:             "RouteCreated",
			Message:            "RouteController created a route",
		},
	}

}

// NodeAddresses returns a list of addresses for the node status
// within Kubernetes.
func (p *Provider) nodeAddresses() []corev1.NodeAddress {
	return []corev1.NodeAddress{
		{
			Type:    "InternalIP",
			Address: p.internalIP,
		},
	}
}

// NodeDaemonEndpoints returns NodeDaemonEndpoints for the node status
// within Kubernetes.
func (p *Provider) nodeDaemonEndpoints() corev1.NodeDaemonEndpoints {
	return corev1.NodeDaemonEndpoints{
		KubeletEndpoint: corev1.DaemonEndpoint{
			Port: p.daemonEndpointPort,
		},
	}
}

/*
// GetStatsSummary returns dummy stats for all pods known by this provider.
func (p *Provider) GetStatsSummary(ctx context.Context) (*stats.Summary, error) {
	var span trace.Span
	ctx, span = trace.StartSpan(ctx, "GetStatsSummary") // nolint: ineffassign,staticcheck
	defer span.End()

	// Grab the current timestamp so we can report it as the time the stats were generated.
	time := metav1.NewTime(time.Now())

	// Create the Summary object that will later be populated with node and pod stats.
	res := &stats.Summary{}

	// Populate the Summary object with basic node stats.
	res.Node = stats.NodeStats{
		NodeName:  p.nodeName,
		StartTime: metav1.NewTime(p.startTime),
	}

	// Populate the Summary object with dummy stats for each pod known by this provider.
	for _, pod := range p.pods {
		var (
			// totalUsageNanoCores will be populated with the sum of the values of UsageNanoCores computes across all containers in the pod.
			totalUsageNanoCores uint64
			// totalUsageBytes will be populated with the sum of the values of UsageBytes computed across all containers in the pod.
			totalUsageBytes uint64
		)

		// Create a PodStats object to populate with pod stats.
		pss := stats.PodStats{
			PodRef: stats.PodReference{
				Name:      pod.Name,
				Namespace: pod.Namespace,
				UID:       string(pod.UID),
			},
			StartTime: pod.CreationTimestamp,
		}

		// Iterate over all containers in the current pod to compute dummy stats.
		for _, container := range pod.Spec.Containers {
			// Grab a dummy value to be used as the total CPU usage.
			// The value should fit a uint32 in order to avoid overflows later on when computing pod stats.
			dummyUsageNanoCores := uint64(rand.Uint32())
			totalUsageNanoCores += dummyUsageNanoCores
			// Create a dummy value to be used as the total RAM usage.
			// The value should fit a uint32 in order to avoid overflows later on when computing pod stats.
			dummyUsageBytes := uint64(rand.Uint32())
			totalUsageBytes += dummyUsageBytes
			// Append a ContainerStats object containing the dummy stats to the PodStats object.
			pss.Containers = append(pss.Containers, stats.ContainerStats{
				Name:      container.Name,
				StartTime: pod.CreationTimestamp,
				CPU: &stats.CPUStats{
					Time:           time,
					UsageNanoCores: &dummyUsageNanoCores,
				},
				Memory: &stats.MemoryStats{
					Time:       time,
					UsageBytes: &dummyUsageBytes,
				},
			})
		}

		// Populate the CPU and RAM stats for the pod and append the PodsStats object to the Summary object to be returned.
		pss.CPU = &stats.CPUStats{
			Time:           time,
			UsageNanoCores: &totalUsageNanoCores,
		}
		pss.Memory = &stats.MemoryStats{
			Time:       time,
			UsageBytes: &totalUsageBytes,
		}
		res.Pods = append(res.Pods, pss)
	}

	// Return the dummy stats.
	return res, nil
}

*/

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

func (p *Provider) activeInitContainers(pod *corev1.Pod) bool {
	activeInitContainers := len(pod.Spec.InitContainers)
	for idx, _ := range pod.Spec.InitContainers {
		if pod.Status.InitContainerStatuses[idx].State.Terminated != nil {
			activeInitContainers--
		}
	}
	return activeInitContainers != 0
}

func (p *Provider) startMainContainers(ctx context.Context, pod *corev1.Pod) {
	now := metav1.NewTime(time.Now())

	for i := range pod.Spec.Containers {
		container := &pod.Spec.Containers[i]

		err := RemoteExecution(p, ctx, api.SUBMIT, pod, container)

		if err != nil {
			pod.Status.ContainerStatuses[i] = corev1.ContainerStatus{
				Name:         container.Name,
				Image:        container.Image,
				Ready:        false,
				RestartCount: 1,
				State: corev1.ContainerState{
					Terminated: &corev1.ContainerStateTerminated{
						Message:   "Could not reach remote cluster",
						StartedAt: now,
						ExitCode:  130,
					},
				},
			}
			pod.Status.Phase = corev1.PodFailed
			continue
		}
		pod.Status.ContainerStatuses[i] = corev1.ContainerStatus{
			Name:         container.Name,
			Image:        container.Image,
			Ready:        true,
			RestartCount: 1,
			State: corev1.ContainerState{
				Running: &corev1.ContainerStateRunning{
					StartedAt: now,
				},
			},
		}

	}
}

// addAttributes adds the specified attributes to the provided span.
// attrs must be an even-sized list of string arguments.
// Otherwise, the span won't be modified.
// TODO: Refactor and move to a "tracing utilities" package.
func addAttributes(ctx context.Context, span trace.Span, attrs ...string) context.Context {
	if len(attrs)%2 == 1 {
		return ctx
	}
	for i := 0; i < len(attrs); i += 2 {
		ctx = span.WithField(ctx, attrs[i], attrs[i+1])
	}
	return ctx
}
