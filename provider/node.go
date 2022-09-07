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
	"runtime"

	"github.com/carv-ics-forth/knoc/api"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// CreateVirtualNode builds a kubernetes node object from a provider
// This is a temporary solution until node stuff actually split off from the provider interface itself.
func (p *Provider) CreateVirtualNode(ctx context.Context, name string, taint *corev1.Taint) *corev1.Node {
	taints := make([]corev1.Taint, 0)

	if taint != nil {
		taints = append(taints, *taint)
	}

	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
				"type":                   "virtual-kubelet",
				"kubernetes.io/role":     "agent",
				"kubernetes.io/os":       runtime.GOOS,
				"kubernetes.io/hostname": name,
				"alpha.service-controller.kubernetes.io/exclude-balancer": "true",
			},
		},
		Spec: corev1.NodeSpec{
			Taints: taints,
		},
		Status: corev1.NodeStatus{
			NodeInfo: corev1.NodeSystemInfo{
				OperatingSystem: runtime.GOOS,
				Architecture:    runtime.GOARCH,
				KubeletVersion:  api.BuildVersion,
			},
			Capacity:        p.Capacity(ctx),
			Allocatable:     p.Capacity(ctx),
			Conditions:      p.NodeConditions(ctx),
			Addresses:       p.NodeAddresses(ctx),
			DaemonEndpoints: *p.NodeDaemonEndpoints(ctx),
		},
	}

	return node
}
