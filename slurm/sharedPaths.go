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

package slurm

import (
	"os"
	"path/filepath"

	"github.com/carv-ics-forth/hpk/api"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
)

// RuntimeDir .hpk/namespace/podName.
func RuntimeDir(podRef api.ObjectKey) string {
	return filepath.Join(api.RuntimeDir, podRef.Namespace, podRef.Name)
}

// MountPaths .hpk/namespace/podName/mountName:mountPath
func MountPaths(podRef api.ObjectKey, mount corev1.VolumeMount) string {
	return filepath.Join(RuntimeDir(podRef), mount.Name+":"+mount.MountPath)
}

// SBatchDirectory .hpk/namespace/podName/.sbatch
func SBatchDirectory(podRef api.ObjectKey) string {
	return filepath.Join(RuntimeDir(podRef), ".sbatch")
}

// SBatchFilePath .hpk/namespace/podName/.sbatch/containerName.sh.
func SBatchFilePath(podRef api.ObjectKey, containerName string) string {
	return filepath.Join(SBatchDirectory(podRef), containerName+".sh")
}

// StdOutputFilePath .hpk/namespace/podName/containerName.stdout.
func StdOutputFilePath(podRef api.ObjectKey, containerName string) string {
	return filepath.Join(RuntimeDir(podRef), containerName+".stdout")
}

// StdErrorFilePath .hpk/namespace/podName/containerName.stderr.
func StdErrorFilePath(podRef api.ObjectKey, containerName string) string {
	return filepath.Join(RuntimeDir(podRef), containerName+".stderr")
}

// ExitCodeFilePath .hpk/namespace/podName/containerName.exitCode.
func ExitCodeFilePath(podRef api.ObjectKey, containerName string) string {
	return filepath.Join(RuntimeDir(podRef), containerName+".exitCode")
}

// JobIDFilePath .hpk/namespace/podName/containerName.jid.
func JobIDFilePath(podRef api.ObjectKey, containerName string) string {
	return filepath.Join(RuntimeDir(podRef), containerName+".jid")
}

// PodSpecFilePath .hpk/namespace/podName/podspec.json.
func PodSpecFilePath(podRef api.ObjectKey) string {
	return filepath.Join(RuntimeDir(podRef), "podspec.json")
}

func createSubDirectory(parent, name string) (string, error) {
	fullPath := filepath.Join(parent, name)

	if err := os.MkdirAll(fullPath, api.PodGlobalDirectoryPermissions); err != nil {
		return fullPath, errors.Wrapf(err, "cannot create dir '%s'", fullPath)
	}

	return fullPath, nil
}
