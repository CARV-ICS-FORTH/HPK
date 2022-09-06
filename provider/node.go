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

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// CreateVirtualNode builds a kubernetes node object from a provider
// This is a temporary solution until node stuff actually split off from the provider interface itself.
func (p *Provider) CreateVirtualNode(ctx context.Context, name string, version string) *corev1.Node {
	taints := make([]corev1.Taint, 0)

	/*
		if taint != nil {
			taints = append(taints, *taint)
		}

	*/

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
				KubeletVersion:  version,
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

/*
// getTaint creates a taint using the provided key/value.
// Taint effect is read from the environment
// The taint key/value may be overwritten by the environment.
func getTaint(c root.Opts) (*corev1.Taint, error) {
	// This can be removed ... or not ...
	// value := c.Provider
	value := "knoc"

	key := c.TaintKey
	if key == "" {
		key = root.DefaultTaintKey
	}

	if c.TaintEffect == "" {
		c.TaintEffect = root.DefaultTaintEffect
	}

	key = root.getEnv("VKUBELET_TAINT_KEY", key)
	value = root.getEnv("VKUBELET_TAINT_VALUE", value)
	effectEnv := root.getEnv("VKUBELET_TAINT_EFFECT", string(c.TaintEffect))

	var effect corev1.TaintEffect
	switch effectEnv {
	case "NoSchedule":
		effect = corev1.TaintEffectNoSchedule
	case "NoExecute":
		effect = corev1.TaintEffectNoExecute
	case "PreferNoSchedule":
		effect = corev1.TaintEffectPreferNoSchedule
	default:
		return nil, errdefs.InvalidInputf("taint effect %q is not supported", effectEnv)
	}

	return &corev1.Taint{
		Key:    key,
		Value:  value,
		Effect: effect,
	}, nil
}


*/