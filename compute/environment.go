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

package compute

import (
	"github.com/carv-ics-forth/hpk/compute/paths"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

// HostEnvironment containers information about the execution environment.
type HostEnvironment struct {
	KubeMasterHost    string
	ContainerRegistry string
	ApptainerBin      string

	EnableCgroupV2 bool

	WorkingDirectory string

	// KubeDNS points to the internal DNS of a Kubernetes cluster.
	KubeDNS string
}

// The VirtualEnvironment create lightweight "virtual environments" that resemble "Pods" semantics.
type VirtualEnvironment struct {
	// PodDirectory points to the pod directory on the underlying filesystem.
	PodDirectory string

	// CgroupFilePath points to the cgroup configuration for the virtual environment.
	CgroupFilePath string

	// ConstructorFilePath points to the script for creating the virtual environment for Pod.
	ConstructorFilePath string

	// IPAddressPath is where we store the internal Pod's ip.
	IPAddressPath string

	// StdoutPath instruct Slurm to write stdout into the specified path.
	StdoutPath string

	// StdoutPath instruct Slurm to write stderr into the specified path.
	StderrPath string

	// SysErrorFilePath indicate a system failure that cause the Pod to fail Immediately, bypassing any other checks.
	SysErrorFilePath string
}

// Instantiated Types
var (
	DefaultLogger = zap.New(zap.UseDevMode(true))
)

var (
	Environment  HostEnvironment
	K8SClient    client.Client
	K8SClientset *kubernetes.Clientset

	HPK paths.HPKPath
)
