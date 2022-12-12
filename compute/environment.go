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

package compute

import (
	"os"
	"path/filepath"
	"regexp"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

type HPCEnvironment struct {
	KubeMasterHost    string
	KubeDNS           string
	ContainerRegistry string
	ApptainerBin      string
}

var Environment HPCEnvironment

var K8SClient client.Client

var DefaultLogger = zap.New(zap.UseDevMode(true))

/************************************************************

			Set Shared Paths for Slurm runtime

************************************************************/

const (
	PodGlobalDirectoryPermissions = 0777
	PodSpecJsonFilePermissions    = 0600
	ContainerJobPermissions       = 0777
)

const (
	RuntimeDir = ".hpk"

	// Slurm-Related Extensions
	ExtensionSysError = ".syserror"

	// Pod-Related Extensions
	ExtensionIP     = ".ip"
	ExtensionCRD    = ".crd"
	ExtensionStdout = ".stdout"
	ExtensionStderr = ".stderr"

	// Container-Related Extensions
	ExtensionExitCode    = ".exitCode"
	ExtensionJobID       = ".jid"
	ExtensionEnvironment = ".env"
	ExtensionLogs        = ".logs"
)

type WalkPodFunc func(path PodPath) error

type PodPath string

func (p PodPath) String() string {
	return string(p)
}

func PodRuntimeDir(podRef client.ObjectKey) PodPath {
	path := filepath.Join(RuntimeDir, podRef.Namespace, podRef.Name)

	return PodPath(path)
}

/*
	Pod-Related paths captured by Slurm Notifier.
	They are necessary to drive the lifecycle of a Pod.
*/

// SysErrorPath .hpk/namespace/podName/.syserror
func (p PodPath) SysErrorPath() string {
	return filepath.Join(string(p), ExtensionSysError)
}

// IDPath .hpk/namespace/podName/.jid
func (p PodPath) IDPath() string {
	return filepath.Join(string(p), ExtensionJobID)
}

// ExitCodePath .hpk/namespace/podName/.exitCode
func (p PodPath) ExitCodePath() string {
	return filepath.Join(string(p), ExtensionExitCode)
}

// IPAddressPath .hpk/namespace/podName/.ip
func (p PodPath) IPAddressPath() string {
	return filepath.Join(string(p), ExtensionIP)
}

/*
	Pod-Related paths not captured by Slurm Notifier.
	They are needed for HPK to bootstrap a pod.
*/

// VirtualEnvironmentDir .hpk/namespace/podName/.virtualenv
func (p PodPath) VirtualEnvironmentDir() PodPath {
	return PodPath(filepath.Join(string(p), ".virtualenv"))
}

// Mountpaths returns .hpk/namespace/podName/.virtualenv/mountName:mountPath
func (p PodPath) Mountpaths(mount corev1.VolumeMount) string {
	return filepath.Join(string(p.VirtualEnvironmentDir()), mount.Name+":"+mount.MountPath)
}

// EncodedJSONPath .hpk/namespace/podName/.virtualenv/pod.crd
func (p PodPath) EncodedJSONPath() string {
	return filepath.Join(string(p.VirtualEnvironmentDir()), "pod"+ExtensionCRD)
}

// ConstructorPath .hpk/namespace/podName/.virtualenv/constructor.sh
func (p PodPath) ConstructorPath() string {
	return filepath.Join(string(p.VirtualEnvironmentDir()), "constructor.sh")
}

// SubmitJobPath .hpk/namespace/podName/.virtualenv/submit.sh
func (p PodPath) SubmitJobPath() string {
	return filepath.Join(string(p.VirtualEnvironmentDir()), "submit.sh")
}

// StdoutPath .hpk/namespace/podName/.virtualenv/pod.stdout
func (p PodPath) StdoutPath() string {
	return filepath.Join(string(p.VirtualEnvironmentDir()), "pod"+ExtensionStdout)
}

// StderrPath .hpk/namespace/podName/.virtualenv/pod.stderr
func (p PodPath) StderrPath() string {
	return filepath.Join(string(p.VirtualEnvironmentDir()), "pod"+ExtensionStderr)
}

/*
	Pod-Related functions
*/

func (p PodPath) CreateSubDirectory(name string) (string, error) {
	fullPath := filepath.Join(string(p.VirtualEnvironmentDir()), name)

	if err := os.MkdirAll(fullPath, PodGlobalDirectoryPermissions); err != nil {
		return fullPath, errors.Wrapf(err, "cannot create dir '%s'", fullPath)
	}

	return fullPath, nil
}

func (p PodPath) CreateFile(name string) (string, error) {
	fullPath := filepath.Join(string(p.VirtualEnvironmentDir()), name)

	f, err := os.Create(fullPath)
	if err != nil {
		return fullPath, errors.Wrapf(err, "cannot create file '%s'", fullPath)
	}

	if err := f.Close(); err != nil {
		return fullPath, errors.Wrapf(err, "cannot close file '%s'", fullPath)
	}

	return fullPath, nil
}

func (p PodPath) PathExists(name string) (os.FileInfo, error) {
	fullPath := filepath.Join(string(p), name)

	return os.Stat(fullPath)
}

/*
	Container-Related paths captured by Slurm Notifier.
	They are necessary to drive the lifecycle of a Container.
*/

func (p PodPath) Container(containerName string) ContainerPath {
	return ContainerPath(filepath.Join(string(p), containerName))
}

type ContainerPath string

func (c ContainerPath) LogsPath() string {
	return filepath.Join(string(c) + ExtensionLogs)
}

func (c ContainerPath) IDPath() string {
	return filepath.Join(string(c) + ExtensionJobID)
}

func (c ContainerPath) ExitCodePath() string {
	return filepath.Join(string(c) + ExtensionExitCode)
}

/*
	Container-Related paths not captured by Slurm Notifier.
	They are needed for HPK to bootstrap a container.
*/

func (c ContainerPath) EnvFilePath() string {
	return filepath.Join(string(c) + ExtensionEnvironment)
}

// ParsePath parses the path according to the expected HPK format, and returns
// the corresponding fields.
// Validated through: https://regex101.com/r/5gRXwJ/1
func ParsePath(path string) (podKey types.NamespacedName, fileName string, invalid bool) {
	re := regexp.MustCompile(`^.hpk/(?P<namespace>\S+)/(?P<pod>\S+?)(/.virtualenv)*/(?P<file>.*)$`)

	match := re.FindStringSubmatch(path)

	if len(match) == 0 {
		invalid = true
		return
	}

	for i, name := range re.SubexpNames() {
		if i > 0 && i <= len(match) {
			switch name {
			case "namespace":
				podKey.Namespace = match[i]
			case "pod":
				podKey.Name = match[i]
			case "file":
				fileName = match[i]
			}
		}
	}

	invalid = false

	return
}
