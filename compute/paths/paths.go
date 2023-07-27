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

package paths

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

/************************************************************

			Set Shared Paths for Slurm runtime

************************************************************/

const (
	PodGlobalDirectoryPermissions = os.FileMode(0o777)
	PodSpecJsonFilePermissions    = os.FileMode(0o600)
	ContainerJobPermissions       = os.FileMode(0o777)
)

const (
	// Slurm-Related Extensions
	ExtensionSysError = ".syserror"

	// Pod-Related Extensions
	ExtensionIP     = ".ip"
	ExtensionCRD    = ".crd"
	ExtensionStdout = ".stdout"
	ExtensionStderr = ".stderr"

	// Container-Related Extensions
	ExtensionExitCode    = ".exitCode"
	ExtensionJobID       = ".jobid"
	ExtensionEnvironment = ".env"
	ExtensionLogs        = ".logs"
)

type HPKPath string

func HPK(rootPath string) HPKPath {
	return HPKPath(filepath.Join(rootPath, ".hpk"))
}

func (p HPKPath) String() string {
	if p == "" {
		panic("HPK path has not been initialized")
	}

	return string(p)
}

func (p HPKPath) ImageDir() string {
	return filepath.Join(string(p), ".images")
}

func (p HPKPath) CorruptedDir() string {
	return filepath.Join(string(p), ".corrupted")
}

type WalkPodFunc func(path PodPath) error

func (p HPKPath) WalkPodDirectories(f WalkPodFunc) error {
	maxDepth := strings.Count(p.String(), string(os.PathSeparator)) + 2 // expect path .hpk/namespace/pod

	return filepath.WalkDir(p.String(), func(path string, info os.DirEntry, err error) error {
		// check for traversing errors
		if err != nil {
			return errors.Wrapf(err, "Pod traversal error")
		}

		// skip files
		if !info.IsDir() {
			return nil
		}

		// skip the systems paths.
		if path == p.CorruptedDir() || path == p.ImageDir() {
			return filepath.SkipDir
		}

		// pod directory is found
		depth := strings.Count(path, string(os.PathSeparator))
		switch {
		case depth < maxDepth: // pod's namespace
			return nil
		case depth == maxDepth: // pod directory
			return f(PodPath(path))
		default: // pod contents. we don't need it. contents should be addressed by PodRuntimeEnv() calls.
			return filepath.SkipDir
		}
	})
}

// ParseAbsPath parses the path according to the expected HPK format, and returns
// the corresponding fields.
// Validated through: https://regex101.com/r/5gRXwJ/2
func (p HPKPath) ParseAbsPath(absPath string) (podKey types.NamespacedName, fileName string, invalid bool) {
	// ignore non absolute paths
	if !filepath.IsAbs(absPath) {
		return types.NamespacedName{}, "", true
	}

	// keep only the last part (e.g, /namespace/pod/.../file)
	relPath := strings.TrimPrefix(absPath, p.String()+string(filepath.Separator))

	// find matches
	re := regexp.MustCompile(`^/*(?P<namespace>\S+)/(?P<pod>\S+?)(/.virtualenv)*/(?P<file>.*)$`)
	match := re.FindStringSubmatch(relPath)
	if len(match) == 0 {
		return types.NamespacedName{}, "", true
	}

	// parse fields
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

	return podKey, fileName, false
}

/*
	Pod-Related paths captured by Slurm Notifier.
	They are necessary to drive the lifecycle of a Pod.
*/

func (p HPKPath) Pod(podRef client.ObjectKey) PodPath {
	path := filepath.Join(p.String(), podRef.Namespace, podRef.Name)

	return PodPath(path)
}

type PodPath string

func (p PodPath) String() string {
	return string(p)
}

// SysErrorFilePath .hpk/namespace/podName/.syserror
func (p PodPath) SysErrorFilePath() string {
	return filepath.Join(string(p), ExtensionSysError)
}

// IPAddressPath .hpk/namespace/podName/.ip
func (p PodPath) IPAddressPath() string {
	return filepath.Join(string(p), ExtensionIP)
}

/*
	Pod-Related paths not captured by Slurm Notifier.
	They are needed for HPK to bootstrap a pod.
*/

// PodEnvironmentIsOK checks if the pod structure is ok, and if it is not, it returns an indiciate reason
func (p PodPath) PodEnvironmentIsOK() (bool, string) {
	// check that there is a valid pod description
	if _, err := os.Open(p.EncodedJSONPath()); err != nil {
		return false, "no pod specification was found"
	}

	// check if the pod is already failed
	if _, err := os.Open(p.SysErrorFilePath()); !os.IsNotExist(err) {
		return false, "pod has failed with a system error"
	}

	return true, ""
}

// VirtualEnvironmentDir .hpk/namespace/podName/.virtualenv
func (p PodPath) VirtualEnvironmentDir() PodPath {
	return PodPath(filepath.Join(string(p), ".virtualenv"))
}

func (p PodPath) VolumeDir() string {
	return filepath.Join(string(p.VirtualEnvironmentDir()), "volumes")
}

// EncodedJSONPath .hpk/namespace/podName/.virtualenv/pod.crd
func (p PodPath) EncodedJSONPath() string {
	return filepath.Join(string(p.VirtualEnvironmentDir()), "pod"+ExtensionCRD)
}

// ConstructorFilePath .hpk/namespace/podName/.virtualenv/constructor.sh
func (p PodPath) ConstructorFilePath() string {
	return filepath.Join(string(p.VirtualEnvironmentDir()), "constructor.sh")
}

// CgroupFilePath .hpk/namespace/podName/.virtualenv/cgroup.toml
func (p PodPath) CgroupFilePath() string {
	return filepath.Join(string(p.VirtualEnvironmentDir()), "cgroup.toml")
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

func (p PodPath) CreateVolume(volumeName string, mode os.FileMode) (string, error) {
	fullPath := filepath.Join(p.VolumeDir(), volumeName)

	if err := os.MkdirAll(fullPath, mode); err != nil {
		return fullPath, errors.Wrapf(err, "cannot create dir '%s'", fullPath)
	}

	return fullPath, nil
}

func (p PodPath) CreateVolumeLink(src string, dst string) (string, error) {
	dstFullPath := filepath.Join(p.VolumeDir(), dst)

	if err := os.Symlink(src, dstFullPath); err != nil {
		return dstFullPath, errors.Wrapf(err, "cannot create symlink '%s'", dstFullPath)
	}

	return dstFullPath, nil
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
