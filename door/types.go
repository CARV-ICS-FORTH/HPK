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

package main

import (
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type DoorContainer struct {
	Name          string                  `json:"name" protobuf:"bytes,1,opt,name=name"`
	Image         string                  `json:"image,omitempty" protobuf:"bytes,2,opt,name=image"`
	Command       []string                `json:"command,omitempty" protobuf:"bytes,3,rep,name=command"`
	Args          []string                `json:"args,omitempty" protobuf:"bytes,4,rep,name=args"`
	WorkingDir    string                  `json:"workingDir,omitempty" protobuf:"bytes,5,opt,name=workingDir"`
	Ports         []v1.ContainerPort      `json:"ports,omitempty" patchStrategy:"merge" patchMergeKey:"containerPort" protobuf:"bytes,6,rep,name=ports"`
	EnvFrom       []v1.EnvFromSource      `json:"envFrom,omitempty" protobuf:"bytes,19,rep,name=envFrom"`
	Env           []v1.EnvVar             `json:"env,omitempty" patchStrategy:"merge" patchMergeKey:"name" protobuf:"bytes,7,rep,name=env"`
	Resources     v1.ResourceRequirements `json:"resources,omitempty" protobuf:"bytes,8,opt,name=resources"`
	VolumeMounts  []v1.VolumeMount        `json:"volumeMounts,omitempty" patchStrategy:"merge" patchMergeKey:"mountPath" protobuf:"bytes,9,rep,name=volumeMounts"`
	VolumeDevices []v1.VolumeDevice       `json:"volumeDevices,omitempty" patchStrategy:"merge" patchMergeKey:"devicePath" protobuf:"bytes,21,rep,name=volumeDevices"`
	Metadata      metav1.ObjectMeta       `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`
}
