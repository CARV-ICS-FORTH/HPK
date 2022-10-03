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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	corev1 "k8s.io/api/core/v1"
)

// Defaults for root command options
const (
	DefaultNodeName             = "virtual-kubelet"
	DefaultInformerResyncPeriod = 1 * time.Minute
	DefaultMetricsAddr          = ":10255"
	DefaultListenPort           = 10250
	DefaultPodSyncWorkers       = 1
	DefaultKubeNamespace        = corev1.NamespaceAll

	DefaultTaintEffect = string(corev1.TaintEffectNoSchedule)
	DefaultTaintKey    = "virtual-kubelet.io/provider"
)

var (
	BuildVersion = "N/A"
	BuildTime    = "N/A"
	K8sVersion   = "v1.25.0"
)

var PauseContainerCommand = []string{"sleep", "infinity"}

const (
	// Provider configuration defaults.
	DefaultCPUCapacity    = "20"
	DefaultMemoryCapacity = "100Gi"
	DefaultPodCapacity    = "20"

	// Values used in tracing as attribute keys.
	NamespaceKey     = "namespace"
	NameKey          = "name"
	ContainerNameKey = "containerName"

	DefaultMaxWorkers   = 10
	DefaultMaxQueueSize = 100

	ExitCodeExtension             = ".exitCode"
	JobIdExtension                = ".jid"
	PauseContainerName            = "pause"
	RuntimeDir                    = ".knoc"
	TemporaryDir                  = ".tmp"
	PodGlobalDirectoryPermissions = 0o766
	PodSpecJsonFilePermissions    = 0o600
	SecretPodDataPermissions      = 0o760
	TemporaryPodDataPermissions   = 0o760
	PodSecretVolPermissions       = 0o755
	PodSecretVolDir               = "/secrets"
	PodSecretFilePermissions      = 0o644
	PodConfigMapVolPermissions    = 0o755
	PodConfigMapVolDir            = "/configmaps"
	PodConfigMapFilePermissions   = 0o644
	PodDownwardApiVolPermissions  = 0o755
	PodDownwardApiVolDir          = "/downwardapis"
	PodDownwardApiFilePermissions = 0o644
	DefaultContainerRegistry      = "docker://"
)

/*
type KNOCProvider struct {
	HPCEnvironment

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

type KNOCConfig struct {
	CPU    string `json:"cpu,omitempty"`
	Memory string `json:"memory,omitempty"`
	Pods   string `json:"pods,omitempty"`
}

*/

// ObjectKey identifies a Kubernetes Object.
type ObjectKey = types.NamespacedName

// ObjectKeyFromObject returns the ObjectKey given a runtime.Object.
func ObjectKeyFromObject(obj metav1.Object) ObjectKey {
	return ObjectKey{Namespace: obj.GetNamespace(), Name: obj.GetName()}
}
