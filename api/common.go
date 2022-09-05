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
// limitations under the License.package common

package api

import (
	"time"

	"github.com/carv-ics-forth/knoc/pkg/manager"
	corev1 "k8s.io/api/core/v1"
)

type Operation string

const (
	SUBMIT = Operation("submit")
	DELETE = Operation("delete")
)

const (
	// Provider configuration defaults.
	DefaultCPUCapacity    = "20"
	DefaultMemoryCapacity = "100Gi"
	DefaultPodCapacity    = "20"

	// Values used in tracing as attribute keys.
	NamespaceKey            = "namespace"
	NameKey                 = "name"
	ContainerNameKey        = "containerName"
	PodVolRoot              = ".knoc/"
	PodSecretVolPerms       = 0755
	PodSecretVolDir         = "/secrets"
	PodSecretFilePerms      = 0644
	PodConfigMapVolPerms    = 0755
	PodConfigMapVolDir      = "/configmaps"
	PodConfigMapFilePerms   = 0644
	PodDownwardApiVolPerms  = 0755
	PodDownwardApiVolDir    = "/downwardapis"
	PodDownwardApiFilePerms = 0644
)

type KNOCProvider struct { // nolint:golint
	NodeName           string
	OperatingSystem    string
	InternalIP         string
	DaemonEndpointPort int32
	Pods               map[string]*corev1.Pod
	Config             KNOCConfig
	StartTime          time.Time
	ResourceManager    *manager.ResourceManager
	Notifier           func(*corev1.Pod)
}

type KNOCConfig struct { // nolint:golint
	CPU    string `json:"cpu,omitempty"`
	Memory string `json:"memory,omitempty"`
	Pods   string `json:"pods,omitempty"`
}

type DoorContainer struct {
	corev1.PodTemplate `json:",omitempty"`

	/*
		Name       string   `json:"name" `
		Image      string   `json:"image,omitempty" `
		Command    []string `json:"command,omitempty"`
		Args       []string `json:"args,omitempty" `
		WorkingDir string   `json:"workingDir,omitempty" `
	*/
}
