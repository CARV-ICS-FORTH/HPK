package slurm

import (
	"os"
	"path/filepath"

	"github.com/carv-ics-forth/knoc/api"
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
