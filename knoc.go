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

package knoc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"math/rand"
	"os"
	"strings"
	"time"

	common "github.com/CARV-ICS-FORTH/knoc/common"

	"github.com/sfreiberg/simplessh"
	"github.com/virtual-kubelet/node-cli/manager"
	"github.com/virtual-kubelet/virtual-kubelet/errdefs"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	"github.com/virtual-kubelet/virtual-kubelet/node/api"
	"github.com/virtual-kubelet/virtual-kubelet/trace"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	stats "k8s.io/kubernetes/pkg/kubelet/apis/stats/v1alpha1"
)

// KNOCProvider implements the virtual-kubelet provider interface and stores pods in memory.
type KNOCProvider struct { // nolint:golint
	nodeName           string
	operatingSystem    string
	internalIP         string
	daemonEndpointPort int32
	pods               map[string]*v1.Pod
	config             KNOCConfig
	startTime          time.Time
	resourceManager    *manager.ResourceManager
	notifier           func(*v1.Pod)
}
type KNOCConfig struct { // nolint:golint
	CPU    string `json:"cpu,omitempty"`
	Memory string `json:"memory,omitempty"`
	Pods   string `json:"pods,omitempty"`
}

// NewProviderConfig creates a new KNOCV0Provider. KNOC legacy provider does not implement the new asynchronous podnotifier interface
func NewProviderConfig(config KNOCConfig, nodeName, operatingSystem string, internalIP string, rm *manager.ResourceManager, daemonEndpointPort int32) (*KNOCProvider, error) {
	// set defaults
	if config.CPU == "" {
		config.CPU = common.DefaultCPUCapacity
	}
	if config.Memory == "" {
		config.Memory = common.DefaultMemoryCapacity
	}
	if config.Pods == "" {
		config.Pods = common.DefaultPodCapacity
	}
	provider := KNOCProvider{
		nodeName:           nodeName,
		operatingSystem:    operatingSystem,
		internalIP:         internalIP,
		daemonEndpointPort: daemonEndpointPort,
		resourceManager:    rm,
		pods:               make(map[string]*v1.Pod),
		config:             config,
		startTime:          time.Now(),
	}

	return &provider, nil
}

// NewProvider creates a new Provider, which implements the PodNotifier interface
func NewProvider(providerConfig, nodeName, operatingSystem string, internalIP string, rm *manager.ResourceManager, daemonEndpointPort int32) (*KNOCProvider, error) {
	config, err := loadConfig(providerConfig, nodeName)
	if err != nil {
		return nil, err
	}
	return NewProviderConfig(config, nodeName, operatingSystem, internalIP, rm, daemonEndpointPort)
}

// loadConfig loads the given json configuration files.
func loadConfig(providerConfig, nodeName string) (config KNOCConfig, err error) {
	data, err := ioutil.ReadFile(providerConfig)
	if err != nil {
		return config, err
	}
	configMap := map[string]KNOCConfig{}
	err = json.Unmarshal(data, &configMap)
	if err != nil {
		return config, err
	}
	if _, exist := configMap[nodeName]; exist {
		config = configMap[nodeName]
		if config.CPU == "" {
			config.CPU = common.DefaultCPUCapacity
		}
		if config.Memory == "" {
			config.Memory = common.DefaultMemoryCapacity
		}
		if config.Pods == "" {
			config.Pods = common.DefaultPodCapacity
		}
	}

	if _, err = resource.ParseQuantity(config.CPU); err != nil {
		return config, fmt.Errorf("Invalid CPU value %v", config.CPU)
	}
	if _, err = resource.ParseQuantity(config.Memory); err != nil {
		return config, fmt.Errorf("Invalid memory value %v", config.Memory)
	}
	if _, err = resource.ParseQuantity(config.Pods); err != nil {
		return config, fmt.Errorf("Invalid pods value %v", config.Pods)
	}
	return config, nil
}

// CreatePod accepts a Pod definition and stores it in memory.
func (p *KNOCProvider) CreatePod(ctx context.Context, pod *v1.Pod) error {
	ctx, span := trace.StartSpan(ctx, "CreatePod")
	var hasInitContainers bool = false
	var state v1.ContainerState
	defer span.End()
	distribution := "docker://"
	// Add the pod's coordinates to the current span.
	ctx = addAttributes(ctx, span, common.NamespaceKey, pod.Namespace, common.NameKey, pod.Name)
	key, err := common.BuildKey(pod)
	if err != nil {
		return err
	}
	now := metav1.NewTime(time.Now())
	running_state := v1.ContainerState{
		Running: &v1.ContainerStateRunning{
			StartedAt: now,
		},
	}
	waiting_state := v1.ContainerState{
		Waiting: &v1.ContainerStateWaiting{
			Reason: "Waiting for InitContainers",
		},
	}
	state = running_state

	// in case we have initContainers we need to stop main containers from executing for now ...
	if len(pod.Spec.InitContainers) > 0 {
		state = waiting_state
		hasInitContainers = true
		// run init container with remote execution enabled
		for _, container := range pod.Spec.InitContainers {
			// MUST TODO: Run init containers sequentialy and NOT all-together
			RemoteExecution(p, ctx, common.CREATE, distribution+container.Image, pod, container)
		}

		pod.Status = v1.PodStatus{
			Phase:     v1.PodRunning,
			HostIP:    "127.0.0.1",
			PodIP:     "127.0.0.1",
			StartTime: &now,
			Conditions: []v1.PodCondition{
				{
					Type:   v1.PodInitialized,
					Status: v1.ConditionFalse,
				},
				{
					Type:   v1.PodReady,
					Status: v1.ConditionFalse,
				},
				{
					Type:   v1.PodScheduled,
					Status: v1.ConditionTrue,
				},
			},
		}
	} else {
		pod.Status = v1.PodStatus{
			Phase:     v1.PodRunning,
			HostIP:    "127.0.0.1",
			PodIP:     "127.0.0.1",
			StartTime: &now,
			Conditions: []v1.PodCondition{
				{
					Type:   v1.PodInitialized,
					Status: v1.ConditionTrue,
				},
				{
					Type:   v1.PodReady,
					Status: v1.ConditionTrue,
				},
				{
					Type:   v1.PodScheduled,
					Status: v1.ConditionTrue,
				},
			},
		}
	}
	// deploy main containers
	for _, container := range pod.Spec.Containers {
		var err error

		if !hasInitContainers {
			err = RemoteExecution(p, ctx, common.CREATE, distribution+container.Image, pod, container)

		}
		if err != nil {
			pod.Status.ContainerStatuses = append(pod.Status.ContainerStatuses, v1.ContainerStatus{
				Name:         container.Name,
				Image:        container.Image,
				Ready:        false,
				RestartCount: 1,
				State: v1.ContainerState{
					Terminated: &v1.ContainerStateTerminated{
						Message:   "Could not reach remote cluster",
						StartedAt: now,
						ExitCode:  130,
					},
				},
			})
			pod.Status.Phase = v1.PodFailed
			continue
		}
		pod.Status.ContainerStatuses = append(pod.Status.ContainerStatuses, v1.ContainerStatus{
			Name:         container.Name,
			Image:        container.Image,
			Ready:        !hasInitContainers,
			RestartCount: 1,
			State:        state,
		})

	}

	p.pods[key] = pod
	p.notifier(pod)

	return nil
}

// UpdatePod accepts a Pod definition and updates its reference.
func (p *KNOCProvider) UpdatePod(ctx context.Context, pod *v1.Pod) error {
	ctx, span := trace.StartSpan(ctx, "UpdatePod")
	defer span.End()

	// Add the pod's coordinates to the current span.
	ctx = addAttributes(ctx, span, common.NamespaceKey, pod.Namespace, common.NameKey, pod.Name)

	log.G(ctx).Infof("receive UpdatePod %q", pod.Name)

	key, err := common.BuildKey(pod)
	if err != nil {
		return err
	}

	p.pods[key] = pod
	p.notifier(pod)

	return nil
}

// DeletePod deletes the specified pod out of memory.
func (p *KNOCProvider) DeletePod(ctx context.Context, pod *v1.Pod) (err error) {
	ctx, span := trace.StartSpan(ctx, "DeletePod")
	defer span.End()

	// Add the pod's coordinates to the current span.
	ctx = addAttributes(ctx, span, common.NamespaceKey, pod.Namespace, common.NameKey, pod.Name)

	log.G(ctx).Infof("receive DeletePod %q", pod.Name)

	key, err := common.BuildKey(pod)
	if err != nil {
		return err
	}

	if _, exists := p.pods[key]; !exists {
		return errdefs.NotFound("pod not found")
	}

	now := metav1.Now()
	pod.Status.Phase = v1.PodSucceeded
	pod.Status.Reason = "KNOCProviderPodDeleted"

	for _, container := range pod.Spec.Containers {
		RemoteExecution(p, ctx, common.DELETE, "", pod, container)
	}
	for _, container := range pod.Spec.InitContainers {
		RemoteExecution(p, ctx, common.DELETE, "", pod, container)
	}
	for idx := range pod.Status.ContainerStatuses {
		pod.Status.ContainerStatuses[idx].Ready = false
		pod.Status.ContainerStatuses[idx].State = v1.ContainerState{
			Terminated: &v1.ContainerStateTerminated{
				Message:    "KNOC provider terminated container upon deletion",
				FinishedAt: now,
				Reason:     "KNOCProviderPodContainerDeleted",
				// StartedAt:  pod.Status.ContainerStatuses[idx].State.Running.StartedAt,
			},
		}
	}
	for idx := range pod.Status.InitContainerStatuses {
		pod.Status.InitContainerStatuses[idx].Ready = false
		pod.Status.InitContainerStatuses[idx].State = v1.ContainerState{
			Terminated: &v1.ContainerStateTerminated{
				Message:    "KNOC provider terminated container upon deletion",
				FinishedAt: now,
				Reason:     "KNOCProviderPodContainerDeleted",
				// StartedAt:  pod.Status.InitContainerStatuses[idx].State.Running.StartedAt,
			},
		}
	}

	p.notifier(pod)
	delete(p.pods, key)

	return nil
}

// GetPod returns a pod by name that is stored in memory.
func (p *KNOCProvider) GetPod(ctx context.Context, namespace, name string) (pod *v1.Pod, err error) {
	ctx, span := trace.StartSpan(ctx, "GetPod")
	defer func() {
		span.SetStatus(err)
		span.End()
	}()

	// Add the pod's coordinates to the current span.
	ctx = addAttributes(ctx, span, common.NamespaceKey, namespace, common.NameKey, name)

	log.G(ctx).Infof("receive GetPod %q", name)

	key, err := common.BuildKeyFromNames(namespace, name)
	if err != nil {
		return nil, err
	}

	if pod, ok := p.pods[key]; ok {
		return pod, nil
	}
	return nil, errdefs.NotFoundf("pod \"%s/%s\" is not known to the provider", namespace, name)
}

// GetContainerLogs retrieves the logs of a container by name from the provider.
func (p *KNOCProvider) GetContainerLogs(ctx context.Context, namespace, podName, containerName string, opts api.ContainerLogOpts) (io.ReadCloser, error) {
	ctx, span := trace.StartSpan(ctx, "GetContainerLogs")
	defer span.End()

	// Add pod and container attributes to the current span.
	ctx = addAttributes(ctx, span, common.NamespaceKey, namespace, common.NameKey, podName, common.ContainerNameKey, containerName)

	log.G(ctx).Infof("receive GetContainerLogs %q", podName)
	client, err := simplessh.ConnectWithKey(os.Getenv("REMOTE_HOST")+":"+os.Getenv("REMOTE_PORT"), os.Getenv("REMOTE_USER"), os.Getenv("REMOTE_KEY"))
	if err != nil {
		panic(err)
	}
	defer client.Close()
	key, err := common.BuildKeyFromNames(namespace, podName)
	if err != nil {
		return nil, err
	}

	pod := p.pods[key]
	instance_name := ""
	for iter := range pod.Spec.InitContainers {
		if pod.Spec.InitContainers[iter].Name == containerName {
			instance_name = BuildRemoteExecutionInstanceName(pod.Spec.InitContainers[iter], pod)
		}
	}
	for iter := range pod.Spec.Containers {
		if pod.Spec.Containers[iter].Name == containerName {
			instance_name = BuildRemoteExecutionInstanceName(pod.Spec.Containers[iter], pod)
		}
	}
	// in case we dont find it or if it hasnt run yet we should return empty string
	output, _ := client.Exec("cat " + ".knoc/" + instance_name + ".out ")

	return ioutil.NopCloser(strings.NewReader(string(output))), nil
}

// RunInContainer executes a command in a container in the pod, copying data
// between in/out/err and the container's stdin/stdout/stderr.
func (p *KNOCProvider) RunInContainer(ctx context.Context, namespace, name, container string, cmd []string, attach api.AttachIO) error {
	client, err := simplessh.ConnectWithKey(os.Getenv("REMOTE_HOST")+":"+os.Getenv("REMOTE_PORT"), os.Getenv("REMOTE_USER"), os.Getenv("REMOTE_KEY"))
	if err != nil {
		panic(err)
	}
	defer client.Close()

	client.Exec(strings.Join(cmd, " "))
	log.G(context.TODO()).Infof("receive ExecInContainer %q", strings.Join(cmd, " "))
	return nil
}

// GetPodStatus returns the status of a pod by name that is "running".
// returns nil if a pod by that name is not found.
func (p *KNOCProvider) GetPodStatus(ctx context.Context, namespace, name string) (*v1.PodStatus, error) {
	ctx, span := trace.StartSpan(ctx, "GetPodStatus")
	defer span.End()

	// Add namespace and name as attributes to the current span.
	ctx = addAttributes(ctx, span, common.NamespaceKey, namespace, common.NameKey, name)

	log.G(ctx).Infof("receive GetPodStatus %q", name)

	pod, err := p.GetPod(ctx, namespace, name)
	if err != nil {
		return nil, err
	}

	return &pod.Status, nil
}

// GetPods returns a list of all pods known to be "running".
func (p *KNOCProvider) GetPods(ctx context.Context) ([]*v1.Pod, error) {
	ctx, span := trace.StartSpan(ctx, "GetPods")
	defer span.End()

	log.G(ctx).Info("receive GetPods")

	var pods []*v1.Pod

	for _, pod := range p.pods {
		pods = append(pods, pod)
	}

	return pods, nil
}

func (p *KNOCProvider) ConfigureNode(ctx context.Context, n *v1.Node) { // nolint:golint
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
func (p *KNOCProvider) capacity() v1.ResourceList {
	return v1.ResourceList{
		"cpu":    resource.MustParse(p.config.CPU),
		"memory": resource.MustParse(p.config.Memory),
		"pods":   resource.MustParse(p.config.Pods),
	}
}

// NodeConditions returns a list of conditions (Ready, OutOfDisk, etc), for updates to the node status
// within Kubernetes.
func (p *KNOCProvider) nodeConditions() []v1.NodeCondition {
	// TODO: Make this configurable
	return []v1.NodeCondition{
		{
			Type:               "Ready",
			Status:             v1.ConditionTrue,
			LastHeartbeatTime:  metav1.Now(),
			LastTransitionTime: metav1.Now(),
			Reason:             "KubeletPending",
			Message:            "kubelet is pending.",
		},
		{
			Type:               "OutOfDisk",
			Status:             v1.ConditionFalse,
			LastHeartbeatTime:  metav1.Now(),
			LastTransitionTime: metav1.Now(),
			Reason:             "KubeletHasSufficientDisk",
			Message:            "kubelet has sufficient disk space available",
		},
		{
			Type:               "MemoryPressure",
			Status:             v1.ConditionFalse,
			LastHeartbeatTime:  metav1.Now(),
			LastTransitionTime: metav1.Now(),
			Reason:             "KubeletHasSufficientMemory",
			Message:            "kubelet has sufficient memory available",
		},
		{
			Type:               "DiskPressure",
			Status:             v1.ConditionFalse,
			LastHeartbeatTime:  metav1.Now(),
			LastTransitionTime: metav1.Now(),
			Reason:             "KubeletHasNoDiskPressure",
			Message:            "kubelet has no disk pressure",
		},
		{
			Type:               "NetworkUnavailable",
			Status:             v1.ConditionFalse,
			LastHeartbeatTime:  metav1.Now(),
			LastTransitionTime: metav1.Now(),
			Reason:             "RouteCreated",
			Message:            "RouteController created a route",
		},
	}

}

// NodeAddresses returns a list of addresses for the node status
// within Kubernetes.
func (p *KNOCProvider) nodeAddresses() []v1.NodeAddress {
	return []v1.NodeAddress{
		{
			Type:    "InternalIP",
			Address: p.internalIP,
		},
	}
}

// NodeDaemonEndpoints returns NodeDaemonEndpoints for the node status
// within Kubernetes.
func (p *KNOCProvider) nodeDaemonEndpoints() v1.NodeDaemonEndpoints {
	return v1.NodeDaemonEndpoints{
		KubeletEndpoint: v1.DaemonEndpoint{
			Port: p.daemonEndpointPort,
		},
	}
}

// GetStatsSummary returns dummy stats for all pods known by this provider.
func (p *KNOCProvider) GetStatsSummary(ctx context.Context) (*stats.Summary, error) {
	var span trace.Span
	ctx, span = trace.StartSpan(ctx, "GetStatsSummary") //nolint: ineffassign,staticcheck
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

// NotifyPods is called to set a pod notifier callback function. This should be called before any operations are done
// within the provider.
func (p *KNOCProvider) NotifyPods(ctx context.Context, f func(*v1.Pod)) {
	p.notifier = f
	go p.statusLoop(ctx)
}

func (p *KNOCProvider) statusLoop(ctx context.Context) {
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

func (p *KNOCProvider) initContainersActive(pod *v1.Pod) bool {
	init_containers_active := len(pod.Spec.InitContainers)
	for idx, _ := range pod.Spec.InitContainers {
		if pod.Status.InitContainerStatuses[idx].State.Terminated != nil {
			init_containers_active--
		}
	}
	return init_containers_active != 0
}

func (p *KNOCProvider) startMainContainers(ctx context.Context, pod *v1.Pod) {
	distribution := "docker://"
	now := metav1.NewTime(time.Now())

	for idx, container := range pod.Spec.Containers {
		err := RemoteExecution(p, ctx, common.CREATE, distribution+container.Image, pod, container)

		if err != nil {
			pod.Status.ContainerStatuses[idx] = v1.ContainerStatus{
				Name:         container.Name,
				Image:        container.Image,
				Ready:        false,
				RestartCount: 1,
				State: v1.ContainerState{
					Terminated: &v1.ContainerStateTerminated{
						Message:   "Could not reach remote cluster",
						StartedAt: now,
						ExitCode:  130,
					},
				},
			}
			pod.Status.Phase = v1.PodFailed
			continue
		}
		pod.Status.ContainerStatuses[idx] = v1.ContainerStatus{
			Name:         container.Name,
			Image:        container.Image,
			Ready:        true,
			RestartCount: 1,
			State: v1.ContainerState{
				Running: &v1.ContainerStateRunning{
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
