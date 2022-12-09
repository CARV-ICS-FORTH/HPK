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
	"fmt"
	"runtime"

	"github.com/matishsiao/goInfo"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// CreateVirtualNode builds a kubernetes node object from a provider
// This is a temporary solution until node stuff actually split off from the provider interface itself.
func (v *VirtualK8S) CreateVirtualNode(ctx context.Context, nodename string, taint *corev1.Taint) *corev1.Node {
	taints := make([]corev1.Taint, 0)

	if taint != nil {
		taints = append(taints, *taint)
	}

	resources := corev1.ResourceList{
		"cpu":    *resource.NewQuantity(int64(runtime.NumCPU()), resource.DecimalSI),
		"memory": resource.MustParse("10Gi"),
		"pods":   resource.MustParse("110"),
	}

	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: nodename,
			Labels: map[string]string{
				"kubernetes.io/hostname": nodename,
				"kubernetes.io/role":     "agent",
				"kubernetes.io/os":       runtime.GOOS,
				"kubernetes.io/arch":     runtime.GOARCH,
				// "type":                   "hpk-kubelet",
				// "alpha.service-controller.kubernetes.io/exclude-balancer": "true",
				// "node.kubernetes.io/exclude-from-external-load-balancers": "true",
			},
			Annotations: map[string]string{
				// "alpha.service-controller.kubernetes.io/exclude-balancer": "true",
			},
		},
		Spec: corev1.NodeSpec{
			// Taints: taints,
		},
		Status: corev1.NodeStatus{
			NodeInfo:        v.NodeSystemInfo(ctx),
			Capacity:        resources,
			Allocatable:     resources,
			Conditions:      NodeConditions(ctx),
			Addresses:       v.NodeAddresses(ctx),
			DaemonEndpoints: v.NodeDaemonEndpoints(ctx),
			Phase:           corev1.NodeRunning,
		},
	}
}

// ConfigureNode enables a provider to configure the node object that
// will be used for Kubernetes.
func (v *VirtualK8S) ConfigureNode(ctx context.Context, node *corev1.Node) {
	/*---------------------------------------------------
	 * Preamble used for Request tracing on the logs
	 *---------------------------------------------------*/
	v.Logger.Info("K8s -> ConfigureNode")
	defer v.Logger.Info("K8s <- ConfigureNode")

	panic("not yet supported")
}

// NodeConditions creates a slice of node conditions representing a
// kubelet in perfect health. These four conditions are the ones which virtual-kubelet
// sets as Unknown when a Ping fails.
func NodeConditions(_ context.Context) []corev1.NodeCondition {
	// NodeConditions returns a list of conditions (Ready, OutOfDisk, etc), for updates to the node status
	// within Kubernetes.
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

func (v *VirtualK8S) NodeAddresses(_ context.Context) []corev1.NodeAddress {
	return []corev1.NodeAddress{
		{
			// Used to server webhook traffic for logs / exec
			Type:    corev1.NodeExternalIP,
			Address: v.InitConfig.InternalIP,
		},
		{
			Type:    corev1.NodeInternalIP,
			Address: v.InitConfig.InternalIP,
		},
	}
}

func (v *VirtualK8S) NodeDaemonEndpoints(_ context.Context) corev1.NodeDaemonEndpoints {
	v.Logger.Info("K8s -> NodeDaemonEndpoints")
	defer v.Logger.Info("K8s <- NodeDaemonEndpoints")

	return corev1.NodeDaemonEndpoints{
		KubeletEndpoint: corev1.DaemonEndpoint{
			Port: v.InitConfig.DaemonPort,
		},
	}
}

func (v *VirtualK8S) NodeSystemInfo(_ context.Context) corev1.NodeSystemInfo {
	// goinfo.GetInfo may crash sometimes. use this method to recover and continue.
	defer func() {
		if r := recover(); r != nil {
			fmt.Println("Recovered from goinfo failure:", r)
		}
	}()

	var kernelVersion string
	var operatingSystem string
	var architecture string

	info, err := goInfo.GetInfo()
	if err != nil {
		kernelVersion = "unknown"
		operatingSystem = runtime.GOOS
		architecture = runtime.GOARCH
	} else {
		kernelVersion = info.Kernel
		operatingSystem = info.OS
		architecture = info.Platform
	}

	return corev1.NodeSystemInfo{
		MachineID:               "",
		SystemUUID:              "",
		BootID:                  "",
		KernelVersion:           kernelVersion,
		OSImage:                 "hpk",
		KubeProxyVersion:        "v1.24.7", // fixme: find it automatically
		KubeletVersion:          v.InitConfig.BuildVersion,
		ContainerRuntimeVersion: "singularity://1.1.3", // fixme: find it automatically
		OperatingSystem:         operatingSystem,
		Architecture:            architecture,
	}
}
