// Copyright © 2023 FORTH-ICS
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

package resources

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

func NewResourceList() corev1.ResourceList {
	return corev1.ResourceList{
		corev1.ResourceCPU:              resource.Quantity{},
		corev1.ResourceMemory:           resource.Quantity{},
		corev1.ResourceStorage:          resource.Quantity{},
		corev1.ResourceEphemeralStorage: resource.Quantity{},
		corev1.ResourcePods:             resource.Quantity{},
	}
}

func Sum(aggr corev1.ResourceList, rlist ...corev1.ResourceList) {
	totalCPU := aggr.Cpu()
	totalMem := aggr.Memory()
	totalStorage := aggr.Storage()
	totalEphemeral := aggr.StorageEphemeral()
	totalPods := aggr.Pods()
	totalGPU := (aggr["gpu"]).DeepCopy()

	gpuQuantity := &totalGPU

	// summarize
	for _, list := range rlist {

		if cpu := list.Cpu(); !cpu.IsZero() {
			totalCPU.Add(*cpu)
		}

		if gpu := list[corev1.ResourceName("nvidia.com/gpu")]; !gpu.IsZero() {
			gpuQuantity.Add(list[corev1.ResourceName("nvidia.com/gpu")])
		}

		if mem := list.Memory(); !mem.IsZero() {
			totalMem.Add(*mem)
		}

		if storage := list.Storage(); !storage.IsZero() {
			totalStorage.Add(*storage)
		}

		if ephemeral := list.StorageEphemeral(); !ephemeral.IsZero() {
			totalEphemeral.Add(*ephemeral)
		}

		if pods := list.Pods(); !pods.IsZero() {
			totalPods.Add(*pods)
		}
	}

	// replace
	aggr[corev1.ResourceCPU] = totalCPU.DeepCopy()
	aggr[corev1.ResourceMemory] = totalMem.DeepCopy()
	aggr[corev1.ResourceStorage] = totalStorage.DeepCopy()
	aggr[corev1.ResourceEphemeralStorage] = totalEphemeral.DeepCopy()
	aggr[corev1.ResourcePods] = totalPods.DeepCopy()
	aggr[corev1.ResourceName("nvidia.com/gpu")] = gpuQuantity.DeepCopy()
}

// ResourceList is a conversion between Kubernetes and Slurm Resource Request abstractions
type ResourceList struct {
	// CPU is the number of requested cpus. Due to the slurm limitations, this can be only integer
	CPU *int64

	// Memory is the number of requests MBs of memory.
	Memory *int64

	GPU *int64
}

func ResourceListToStruct(list corev1.ResourceList) ResourceList {
	var rlist ResourceList

	if cpu := list.Cpu(); !cpu.IsZero() {
		val := cpu.Value()
		rlist.CPU = &val
	}

	if gpu := list[corev1.ResourceName("nvidia.com/gpu")]; !gpu.IsZero() {
		val := gpu.Value()
		rlist.GPU = &val
	}

	if mem := list.Memory(); !mem.IsZero() {
		val := mem.ScaledValue(resource.Mega)
		rlist.Memory = &val
	}

	return rlist
}
