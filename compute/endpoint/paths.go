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

package endpoint

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	PodGlobalDirectoryPermissions = os.FileMode(0o777)
	PodSpecJsonFilePermissions    = os.FileMode(0o600)
	ContainerJobPermissions       = os.FileMode(0o777)
)

type ControlFileType = string

// Control File (Written by the Sbatch script)
const (
	// ExtensionSysError describes the file where the sbatch will describe its failure.
	ExtensionSysError ControlFileType = ".syserror"

	// ExtensionIP describes the file where the sbatch script will write its ip.
	ExtensionIP ControlFileType = ".ip"

	// ExtensionExitCode describes the file where the sbatch script will write its exit code.
	ExtensionExitCode ControlFileType = ".exitCode"

	// ExtensionJobID describes the file  where the sbatch script will write its job id.
	ExtensionJobID ControlFileType = ".jobid"
)

// Pod-Related Extensions
const (
	// ExtensionCRD describes the file where HPK will write the pod definition.
	ExtensionCRD = ".crd"

	// ExtensionStdout describes the file where the sbatch script will write its stdout.
	ExtensionStdout = ".stdout"

	// ExtensionStderr describes the file where the sbatch script will write its stderr.
	ExtensionStderr = ".stderr"
)

// Container-Related Extensions
const (
	// ExtensionEnvironment describes the file  where the environment variables for the container are held.
	ExtensionEnvironment = ".env"

	// ExtensionLogs describes the file  where the sbatch script will write its logs.
	ExtensionLogs = ".logs"
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

// ParseControlFilePath parses the path according to the expected HPK format, and returns the corresponding fields.
// Validated through: https://regex101.com/r/olnlMx/1
func (p HPKPath) ParseControlFilePath(absPath string) (podKey types.NamespacedName, fileName string, invalid bool) {
	// ignore non absolute paths
	if !filepath.IsAbs(absPath) {
		return types.NamespacedName{}, "", true
	}

	// keep only the relative path within the pod directory (e.g, /namespace/pod/.../file)
	relPath := strings.TrimPrefix(absPath, p.String())

	// find matches
	re := regexp.MustCompile(`^/(?P<namespace>\S+)/(?P<pod>\S+?)/controlfiles/(?P<file>.*)$`)
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

// JobDir .hpk/namespace/podName/.virtualenv
func (p PodPath) JobDir() string {
	return filepath.Join(string(p), "job")
}

func (p PodPath) VolumeDir() string {
	return filepath.Join(string(p), "volumes")
}

func (p PodPath) LogDir() string {
	return filepath.Join(string(p), "logs")
}

func (p PodPath) ControlFileDir() string {
	return filepath.Join(string(p), "controlfiles")
}

// EncodedJSONPath .hpk/namespace/podName/.virtualenv/pod.crd
func (p PodPath) EncodedJSONPath() string {
	return filepath.Join(p.JobDir(), "pod"+ExtensionCRD)
}

// ConstructorFilePath .hpk/namespace/podName/.virtualenv/constructor.sh
func (p PodPath) ConstructorFilePath() string {
	return filepath.Join(p.JobDir(), "constructor.sh")
}

// CgroupFilePath .hpk/namespace/podName/.virtualenv/cgroup.toml
func (p PodPath) CgroupFilePath() string {
	return filepath.Join(p.JobDir(), "cgroup.toml")
}

// SubmitJobPath .hpk/namespace/podName/.virtualenv/submit.sh
func (p PodPath) SubmitJobPath() string {
	return filepath.Join(p.JobDir(), "submit.sh")
}

// StdoutPath $HPK/<namespace>/<podName>/logs/.stdout
func (p PodPath) StdoutPath() string {
	return filepath.Join(p.LogDir(), ExtensionStdout)
}

// StderrPath $HPK/<namespace>/<podName>/logs/.stderr
func (p PodPath) StderrPath() string {
	return filepath.Join(p.LogDir(), ExtensionStderr)
}

// SysErrorFilePath points to $HPK/<namespace>/<podName>/controlfile/.syserror
func (p PodPath) SysErrorFilePath() string {
	return filepath.Join(p.ControlFileDir(), string(ExtensionSysError))
}

// IPAddressPath points $HPK/<namespace>/<podName>/controlfile/.ip
func (p PodPath) IPAddressPath() string {
	return filepath.Join(p.ControlFileDir(), string(ExtensionIP))
}

/*
	Container-Related paths captured by Slurm Notifier.
	They are necessary to drive the lifecycle of a Container.
*/

func (p PodPath) Container(containerName string) ContainerPath {
	return ContainerPath{
		p:             p,
		containerName: containerName,
	}
}

type ContainerPath struct {
	p             PodPath
	containerName string
}

func (c ContainerPath) LogsPath() string {
	return filepath.Join(c.p.LogDir(), c.containerName+ExtensionLogs)
}

func (c ContainerPath) IDPath() string {
	return filepath.Join(c.p.ControlFileDir(), c.containerName+string(ExtensionJobID))
}

func (c ContainerPath) ExitCodePath() string {
	return filepath.Join(c.p.ControlFileDir(), c.containerName+string(ExtensionExitCode))
}

/*
	Container-Related paths not captured by Slurm Notifier.
	They are needed for HPK to bootstrap a container.
*/

func (c ContainerPath) EnvFilePath() string {
	return filepath.Join(c.p.JobDir(), c.containerName+ExtensionEnvironment)
}
