package paths

import (
	"path/filepath"

	"github.com/carv-ics-forth/knoc/api"
	corev1 "k8s.io/api/core/v1"
)

// RuntimeDir .knoc/namespace/podName.
func RuntimeDir(podRef api.ObjectKey) string {
	return filepath.Join(api.RuntimeDir, podRef.Namespace, podRef.Name)
}

// TemporaryDir .tmp/namespace/podName.
func TemporaryDir(podRef api.ObjectKey) string {
	return filepath.Join(api.TemporaryDir, podRef.Namespace, podRef.Name)
}

func GenerateMountPaths(podRef api.ObjectKey, mount corev1.VolumeMount) string {
	return filepath.Join(RuntimeDir(podRef), mount.Name+":"+mount.MountPath)
}

// ScriptFilePath .tmp/namespace/podName/containerName.sh.
func ScriptFilePath(podRef api.ObjectKey, containerName string) string {
	return filepath.Join(TemporaryDir(podRef), containerName+".sh")
}

// StdOutputFilePath .knoc/namespace/podName/containerName.stdout.
func StdOutputFilePath(podRef api.ObjectKey, containerName string) string {
	return filepath.Join(RuntimeDir(podRef), containerName+".stdout")
}

// StdErrorFilePath .knoc/namespace/podName/containerName.stderr.
func StdErrorFilePath(podRef api.ObjectKey, containerName string) string {
	return filepath.Join(RuntimeDir(podRef), containerName+".stderr")
}

// ExitCodeFilePath .knoc/namespace/podName/containerName.exitCode.
func ExitCodeFilePath(podRef api.ObjectKey, containerName string) string {
	return filepath.Join(RuntimeDir(podRef), containerName+".exitCode")
}

// JobIDFilePath .knoc/namespace/podName/containerName.jid.
func JobIDFilePath(podRef api.ObjectKey, containerName string) string {
	return filepath.Join(RuntimeDir(podRef), containerName+".jid")
}

// PodSpecFilePath .knoc/namespace/podName/podName.json.
func PodSpecFilePath(podRef api.ObjectKey) string {
	return filepath.Join(RuntimeDir(podRef), podRef.Name+".json")
}

// JobName podName/containerName.
func JobName(podRef api.ObjectKey, container *corev1.Container) string {
	return podRef.Name + "/" + container.Name
}
