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
	"fmt"
	"runtime"

	"github.com/carv-ics-forth/knoc/api"
	"github.com/matishsiao/goInfo"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// CreateVirtualNode builds a kubernetes node object from a provider
// This is a temporary solution until node stuff actually split off from the provider interface itself.
func (p *Provider) CreateVirtualNode(ctx context.Context, name string, taint *corev1.Taint) *corev1.Node {
	taints := make([]corev1.Taint, 0)

	if taint != nil {
		taints = append(taints, *taint)
	}

	resources := corev1.ResourceList{
		"cpu":    resource.MustParse("30"),
		"memory": resource.MustParse("10Gi"),
		"pods":   resource.MustParse("100"),
	}

	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
				"type":                   "virtual-kubelet",
				"kubernetes.io/role":     "agent",
				"kubernetes.io/os":       runtime.GOOS,
				"kubernetes.io/hostname": name,
				"alpha.service-controller.kubernetes.io/exclude-balancer": "true",
				"node.kubernetes.io/exclude-from-external-load-balancers": "true",
			},
		},
		Spec: corev1.NodeSpec{
			Taints: taints,
		},
		Status: corev1.NodeStatus{
			Capacity:        resources,
			Allocatable:     resources,
			Phase:           corev1.NodeRunning,
			Conditions:      NodeConditions(ctx),
			Addresses:       p.NodeAddresses(ctx),
			DaemonEndpoints: p.NodeDaemonEndpoints(ctx),
			NodeInfo:        p.NodeSystemInfo(ctx),
			Images:          nil,
			VolumesInUse:    nil,
			VolumesAttached: nil,
			Config:          nil,
		},
	}
}

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

func (p *Provider) NodeAddresses(_ context.Context) []corev1.NodeAddress {
	return []corev1.NodeAddress{{
		Type:    "InternalIP",
		Address: p.InitConfig.InternalIP,
	}}
}

func (p *Provider) NodeDaemonEndpoints(_ context.Context) corev1.NodeDaemonEndpoints {
	p.Logger.Info("-> NodeDaemonEndpoints")
	defer p.Logger.Info("<- NodeDaemonEndpoints")

	return corev1.NodeDaemonEndpoints{
		KubeletEndpoint: corev1.DaemonEndpoint{
			Port: p.InitConfig.DaemonPort,
		},
	}
}

func (p *Provider) NodeSystemInfo(_ context.Context) corev1.NodeSystemInfo {
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
		OSImage:                 "knoc",
		ContainerRuntimeVersion: "vkubelet://6.6.6.6",
		KubeletVersion:          api.BuildVersion,
		KubeProxyVersion:        "",
		OperatingSystem:         operatingSystem,
		Architecture:            architecture,
	}
}
