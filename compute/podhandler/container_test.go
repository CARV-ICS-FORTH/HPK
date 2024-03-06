package podhandler

import (
	"testing"
)

func Test_PodHandler_buildContainer(t *testing.T) {
	/*
		var container corev1.Container

		container.Env = []corev1.EnvVar{
			{
				Name:  "POD_NAME",
				Value: "foo",
			},
			{
				Name: "ANNOTATION",
				ValueFrom: &corev1.EnvVarSource{
					FieldRef: &corev1.ObjectFieldSelector{
						APIVersion: "v1",
						FieldPath:  "metadata.annotations['mysubpath']",
					},
				},
			},
		}

		container.VolumeMounts = []corev1.VolumeMount{
			{
				Name:        "workdir1",
				MountPath:   "/subpath_mount",
				SubPathExpr: "$(ANNOTATION)/$(POD_NAME)",
			},
			{
				Name:      "workdir1",
				MountPath: "/volume_mount",
			},
		}

		var pod corev1.Pod
		var containerStatus corev1.ContainerStatus

		h := &PodHandler{
			Pod:             &pod,
			podEnvVariables: []corev1.EnvVar{},
			podDirectory:    "",
			logger:          logr.Logger{},
		}

		_, err := h.buildContainer(&container, &containerStatus)
		if err != nil {
			t.Fatal(err)
		}

	*/
}
