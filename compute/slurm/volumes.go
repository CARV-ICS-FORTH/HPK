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
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/carv-ics-forth/hpk/compute"
	"github.com/carv-ics-forth/hpk/pkg/fieldpath"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	k8errors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
)

// NotFoundBackoff is the recommended backoff for a resource that is required,
// but is not created yet. For instance, when mounting configmap volumes to pods.
// TODO: in future version, the backoff can be self-modified depending on the load of the controller.
var NotFoundBackoff = wait.Backoff{
	Steps:    4,
	Duration: 2 * time.Second,
	Factor:   5.0,
	Jitter:   0.1,
}

func (h *podHandler) prepareVolumes(ctx context.Context) {
	/*---------------------------------------------------
	 * Copy volumes from local Pod to remote HPCBackend
	 *---------------------------------------------------*/
	for _, vol := range h.Pod.Spec.Volumes {
		switch {
		case vol.VolumeSource.EmptyDir != nil:
			/*---------------------------------------------------
			 * EmptyDir
			 *---------------------------------------------------*/
			emptyDir, err := h.podDirectory.CreateSubDirectory(vol.Name)
			if err != nil {
				SystemError(err, "cannot create dir '%s' for emptyDir", emptyDir)
			}
			// without size limit for now

		case vol.VolumeSource.ConfigMap != nil:
			/*---------------------------------------------------
			 * ConfigMap
			 *---------------------------------------------------*/
			h.ConfigMapVolumeSource(ctx, vol)

		case vol.VolumeSource.Secret != nil:
			/*---------------------------------------------------
			 * Secret
			 *---------------------------------------------------*/
			h.SecretVolumeSource(ctx, vol)

		case vol.VolumeSource.DownwardAPI != nil:
			/*---------------------------------------------------
			 * Downward API
			 *---------------------------------------------------*/
			h.DownwardAPIVolumeSource(ctx, vol)

		case vol.VolumeSource.HostPath != nil:
			/*---------------------------------------------------
			 * HostPath
			 *---------------------------------------------------*/
			h.HostPathVolumeSource(ctx, vol)

		case vol.VolumeSource.PersistentVolumeClaim != nil:
			/*---------------------------------------------------
			 * Persistent Volume Claim
			 *---------------------------------------------------*/
			h.PersistentVolumeClaimSource(ctx, vol)

		case vol.VolumeSource.Projected != nil:
			/*---------------------------------------------------
			 * Projected
			 *---------------------------------------------------*/
			h.ProjectedVolumeSource(ctx, vol)

		default:
			logrus.Warn(vol)

			panic("It seems I have missed a Volume type")
		}
	}
}

func (h *podHandler) ConfigMapVolumeSource(ctx context.Context, vol corev1.Volume) {
	/*---------------------------------------------------
	 * ConfigMap
	 * Example:
		volumes:
		 - name: config-volume
		   configMap:
			 name: game-config
	 *---------------------------------------------------*/

	var configmap corev1.ConfigMap

	source := vol.VolumeSource.ConfigMap

	key := types.NamespacedName{Namespace: h.Pod.GetNamespace(), Name: source.Name}

	{ // get the resource
		err := retry.OnError(NotFoundBackoff, k8errors.IsNotFound,
			func() error {
				return compute.K8SClient.Get(ctx, key, &configmap)
			})

		if err != nil {
			if k8errors.IsNotFound(err) {
				h.logger.Error(err, "MountVolume has failed for configmap")

				if source.Optional == nil || *source.Optional == true {
					PodError(h.Pod, ReasonObjectNotFound, "configmap '%s'", key)

					return
				}
				/*-- configmap is optional, and we can safely skip the step --*/
			} else {
				SystemError(err, "error getting configmap '%s'", key)
			}
		}
	}

	// .hpk/namespace/podName/.virtualenv/volName/*
	podConfigMapDir, err := h.podDirectory.CreateSubDirectory(vol.Name)
	if err != nil {
		SystemError(err, "cannot create dir '%s' for configmap", podConfigMapDir)
	}

	for k, v := range configmap.Data {
		// TODO: Ensure that these files are deleted in failure cases
		fullPath := filepath.Join(podConfigMapDir, k)

		if err := os.WriteFile(fullPath, []byte(v), fs.FileMode(*source.DefaultMode)); err != nil {
			SystemError(err, "cannot write configmap file '%s'", fullPath)
		}
	}
}

func (h *podHandler) SecretVolumeSource(ctx context.Context, vol corev1.Volume) {
	var secret corev1.Secret

	source := vol.VolumeSource.Secret

	key := types.NamespacedName{Namespace: h.Pod.GetNamespace(), Name: source.SecretName}

	{ // get the resource
		err := retry.OnError(NotFoundBackoff, k8errors.IsNotFound,
			func() error {
				return compute.K8SClient.Get(ctx, key, &secret)
			})

		if err != nil {
			if k8errors.IsNotFound(err) {
				h.logger.Error(err, "MountVolume has failed for secret")

				if source.Optional == nil || *source.Optional == true {
					PodError(h.Pod, ReasonObjectNotFound, "secret '%s'", key)

					return
				}
				/*-- secret is optional, and we can safely skip the step --*/
			} else {
				SystemError(err, "error getting secret '%s'", key)
			}
		}
	}

	// .hpk/namespace/podName/.virtualenv/secretName/*
	podSecretDir, err := h.podDirectory.CreateSubDirectory(vol.Name)
	if err != nil {
		SystemError(err, "cannot create dir '%s' for secrets", podSecretDir)
	}

	for k, v := range secret.Data {
		fullPath := filepath.Join(podSecretDir, k)

		if err := os.WriteFile(fullPath, v, fs.FileMode(*source.DefaultMode)); err != nil {
			SystemError(err, "Could not write secret file %s", fullPath)
		}
	}
}

func (h *podHandler) DownwardAPIVolumeSource(ctx context.Context, vol corev1.Volume) {
	downApiDir, err := h.podDirectory.CreateSubDirectory(vol.Name)
	if err != nil {
		SystemError(err, "cannot create dir '%s' for downwardApi", downApiDir)
	}

	for _, item := range vol.DownwardAPI.Items {
		itemPath := filepath.Join(downApiDir, item.Path)
		value, err := fieldpath.ExtractFieldPathAsString(h.Pod, item.FieldRef.FieldPath)
		if err != nil {
			PodError(h.Pod, ReasonSpecError, err.Error())

			return
		}

		if err := os.WriteFile(itemPath, []byte(value), fs.FileMode(*vol.Projected.DefaultMode)); err != nil {
			SystemError(err, "cannot write config map file '%s'", itemPath)
		}
	}
}

func (h *podHandler) HostPathVolumeSource(ctx context.Context, vol corev1.Volume) {
	switch *vol.VolumeSource.HostPath.Type {
	case corev1.HostPathUnset:
		// For backwards compatible, leave it empty if unset
		if path, err := h.podDirectory.CreateSubDirectory(vol.Name); err != nil {
			SystemError(err, "cannot create HostPathUnset at path '%s'", path)
		}

	case corev1.HostPathDirectoryOrCreate:
		// If nothing exists at the given path, an empty directory will be created there
		// as needed with file mode 0755, having the same group and ownership with Kubelet.
		if path, err := h.podDirectory.CreateSubDirectory(vol.Name); err != nil {
			SystemError(err, "cannot create HostPathDirectoryOrCreate at path '%s'", path)
		}
	case corev1.HostPathDirectory:
		// A directory must exist at the given path

		info, err := h.podDirectory.PathExists(vol.Name)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				PodError(h.Pod, "VolumeNotFound", "volume '%s' was not found", vol.Name)
				return
			}
			SystemError(err, "failed to inspect volume '%s'", vol.Name)
		}

		if !info.Mode().IsDir() {
			PodError(h.Pod, "UnexpectedVolumeType", "volume '%s' was expected to be directory", vol.Name)
			return
		}

	case corev1.HostPathFileOrCreate:
		// If nothing exists at the given path, an empty file will be created there
		// as needed with file mode 0644, having the same group and ownership with Kubelet.

		_, err := h.podDirectory.PathExists(vol.Name)
		if err == nil {
			return
		}

		if errors.Is(err, os.ErrNotExist) {
			if path, err := h.podDirectory.CreateFile(vol.Name); err != nil {
				SystemError(err, "cannot apply HostPathDirectoryOrCreate at path '%s'", path)
			}
		} else {
			SystemError(err, "failed to inspect volume '%s'", vol.Name)
		}

	case corev1.HostPathFile:
		// A file must exist at the given path

		info, err := h.podDirectory.PathExists(vol.Name)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				PodError(h.Pod, "VolumeNotFound", "volume '%s' was not found", vol.Name)
				return
			}
			SystemError(err, "failed to inspect volume '%s'", vol.Name)
		}

		if !info.Mode().IsRegular() {
			PodError(h.Pod, "UnexpectedVolumeType", "volume '%s' was expected to be file", vol.Name)
			return
		}

	case corev1.HostPathSocket, corev1.HostPathCharDev, corev1.HostPathBlockDev:
		// A UNIX socket/char device/ block device must exist at the given path

		info, err := h.podDirectory.PathExists(vol.Name)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				PodError(h.Pod, "VolumeNotFound", "volume '%s' was not found", vol.Name)
				return
			}
			SystemError(err, "failed to inspect volume '%s'", vol.Name)
		}

		// todo: perform the checks
		_ = info
	default:
		logrus.Warn(vol.Projected)

		panic("It seems I have missed a ProjectedVolume type")
	}
}

func (h *podHandler) PersistentVolumeClaimSource(ctx context.Context, vol corev1.Volume) {
	var pvc corev1.PersistentVolumeClaim

	source := vol.VolumeSource.PersistentVolumeClaim

	key := types.NamespacedName{Namespace: h.Pod.GetNamespace(), Name: source.ClaimName}

	if err := retry.OnError(NotFoundBackoff, k8errors.IsNotFound,
		func() error {
			return compute.K8SClient.Get(ctx, key, &pvc)
		}); err != nil {
		SystemError(err, "error getting persistentVolumeClaim '%s'", key)
	}

	// fixme: not sure if this is the desired behavior, or if we should create a symlink to another directory.
	if path, err := h.podDirectory.CreateSubDirectory(vol.Name); err != nil {
		SystemError(err, "cannot create persistentVolumeClaim at path '%s'", path)
	}
}

func (h *podHandler) ProjectedVolumeSource(ctx context.Context, vol corev1.Volume) {
	projectedVolPath, err := h.podDirectory.CreateSubDirectory(vol.Name)
	if err != nil {
		SystemError(err, "cannot create dir '%s' for projected volume", projectedVolPath)
	}

	for _, projectedSrc := range vol.Projected.Sources {
		switch {
		case projectedSrc.DownwardAPI != nil:
			/*---------------------------------------------------
			 * Projected DownwardAPI
			 *---------------------------------------------------*/
			for _, item := range projectedSrc.DownwardAPI.Items {
				itemPath := filepath.Join(projectedVolPath, item.Path)

				value, err := fieldpath.ExtractFieldPathAsString(h.Pod, item.FieldRef.FieldPath)
				if err != nil {
					PodError(h.Pod, ReasonSpecError, err.Error())

					return
				}

				if err := os.WriteFile(itemPath, []byte(value), fs.FileMode(*vol.Projected.DefaultMode)); err != nil {
					SystemError(err, "cannot write config map file '%s'", itemPath)
				}
			}

		case projectedSrc.ServiceAccountToken != nil:
			/*---------------------------------------------------
			 * Projected ServiceAccountToken
			 *---------------------------------------------------*/
			var serviceAccount corev1.ServiceAccount

			key := types.NamespacedName{Namespace: h.Pod.GetNamespace(), Name: h.Pod.Spec.ServiceAccountName}

			if err := retry.OnError(NotFoundBackoff, k8errors.IsNotFound,
				func() error {
					return compute.K8SClient.Get(ctx, key, &serviceAccount)
				}); err != nil {
				SystemError(err, "error getting serviceaccount '%s'", key)
			}

			/*
				automount follows the instructions of:
				https://kubernetes.io/docs/tasks/configure-pod-container/configure-service-account/
			*/

			automount := true

			switch {
			case h.Pod.Spec.AutomountServiceAccountToken != nil:
				automount = *h.Pod.Spec.AutomountServiceAccountToken
			case serviceAccount.AutomountServiceAccountToken != nil:
				automount = *serviceAccount.AutomountServiceAccountToken
			}

			if !automount {
				continue
			}

			for _, secretRef := range serviceAccount.Secrets {
				var secret corev1.Secret

				secretKey := types.NamespacedName{Namespace: h.Pod.GetNamespace(), Name: secretRef.Name}

				if err := retry.OnError(NotFoundBackoff, k8errors.IsNotFound,
					func() error {
						return compute.K8SClient.Get(ctx, secretKey, &secret)
					}); err != nil {
					SystemError(err, "error getting projected.secret '%s'", key)
				}

				// TODO: Update upon exceeded expiration date
				fullPath := filepath.Join(projectedVolPath, projectedSrc.ServiceAccountToken.Path)

				if err := os.WriteFile(fullPath, secret.Data[projectedSrc.ServiceAccountToken.Path], fs.FileMode(0o766)); err != nil {
					SystemError(err, "cannot write token '%s'", fullPath)
				}
			}

			/*
				source := projectedSrc.ServiceAccountToken

				tokenRequest, err := compute.ClientSet.CoreV1().
					ServiceAccounts(h.Pod.GetNamespace()).
					CreateToken(ctx, h.Pod.Spec.ServiceAccountName, &authv1.TokenRequest{
						Spec: authv1.TokenRequestSpec{
							Audiences:         []string{source.Audience},
							ExpirationSeconds: source.ExpirationSeconds,
							BoundObjectRef: &authv1.BoundObjectReference{
								Kind:       "Pod",
								APIVersion: "v1",
								Name:       h.Pod.GetName(),
							},
						},
					}, metav1.CreateOptions{})

				if err != nil {
					return errors.Wrapf(err, "failed to create token for Pod '%s'", h.podKey)
				}

				// TODO: Update upon exceeded expiration date
				// TODO: Ensure that these files are deleted in failure cases
				fullPath := filepath.Join(projectedVolPath, projectedSrc.ServiceAccountToken.Path)

				if err := os.WriteFile(fullPath, []byte(tokenRequest.Status.Token), fs.FileMode(0o766)); err != nil {
					return errors.Wrapf(err, "cannot write token '%s'", fullPath)
				}


					var automount bool

					switch {
					case h.Pod.Spec.AutomountServiceAccountToken != nil:
						automount = *h.Pod.Spec.AutomountServiceAccountToken
					case serviceAccount.AutomountServiceAccountToken != nil:
						automount = *serviceAccount.AutomountServiceAccountToken
					}

					if automount {
						for _, secretRef := range serviceAccount.Secrets {
							var secret corev1.Secret

							secretKey := types.NamespacedName{Namespace: secretRef.Namespace, Name: secretRef.Name}

							if err := compute.K8SClient.Get(ctx, secretKey, &secret); err != nil {
								return errors.Wrapf(err, "failed to get secret '%s'", secretKey)
							}

							// TODO: Update upon exceeded expiration date
							// TODO: Ensure that these files are deleted in failure cases
							fullPath := filepath.Join(projectedVolPath, projectedSrc.ServiceAccountToken.Path)

							if err := os.WriteFile(fullPath, secret.Data[projectedSrc.ServiceAccountToken.Path], fs.FileMode(0o766)); err != nil {
								return errors.Wrapf(err, "cannot write config map file '%s'", fullPath)
							}
						}
					}
			*/

		case projectedSrc.ConfigMap != nil:
			/*---------------------------------------------------
			 * Projected ConfigMap
			 *---------------------------------------------------*/
			var configmap corev1.ConfigMap

			source := projectedSrc.ConfigMap

			key := types.NamespacedName{Namespace: h.Pod.GetNamespace(), Name: source.Name}

			{ // get the resource
				err := retry.OnError(NotFoundBackoff, k8errors.IsNotFound,
					func() error {
						return compute.K8SClient.Get(ctx, key, &configmap)
					})

				if err != nil {
					if k8errors.IsNotFound(err) {
						h.logger.Error(err, "MountVolume has failed for projected.configmap")

						if source.Optional == nil || *source.Optional == true {
							PodError(h.Pod, ReasonObjectNotFound, "configmap '%s'", key)

							return
						}
						/*-- configmap is optional, and we can safely skip the step --*/
					} else {
						SystemError(err, "error getting configmap '%s'", key)
					}
				}
			}

			for k, item := range configmap.Data {
				// TODO: Ensure that these files are deleted in failure cases
				itemPath := filepath.Join(projectedVolPath, k)
				if err := os.WriteFile(itemPath, []byte(item), compute.PodGlobalDirectoryPermissions); err != nil {
					SystemError(err, "cannot write config map file '%s'", itemPath)
				}
			}

		case projectedSrc.Secret != nil:
			/*---------------------------------------------------
			 * Projected Secret
			 *---------------------------------------------------*/
			var secret corev1.Secret

			source := projectedSrc.Secret

			key := types.NamespacedName{Namespace: h.Pod.GetNamespace(), Name: source.Name}

			{ // get the resource
				err := retry.OnError(NotFoundBackoff, k8errors.IsNotFound,
					func() error {
						return compute.K8SClient.Get(ctx, key, &secret)
					})

				if err != nil {
					if k8errors.IsNotFound(err) {
						h.logger.Error(err, "MountVolume has failed for projected.secret")

						if source.Optional == nil || *source.Optional == true {
							PodError(h.Pod, ReasonObjectNotFound, "secret '%s'", key)

							return
						}
						/*-- secret is optional, and we can safely skip the step --*/
					} else {
						SystemError(err, "error getting secret '%s'", key)
					}
				}
			}

			for k, item := range secret.Data {
				// TODO: Ensure that these files are deleted in failure cases
				itemPath := filepath.Join(projectedVolPath, k)

				if err := os.WriteFile(itemPath, item, compute.PodGlobalDirectoryPermissions); err != nil {
					SystemError(err, "cannot write config map file '%s'", itemPath)
				}
			}
		default:
			logrus.Warn(projectedSrc)

			panic("Have I missed something ")
		}
	}
}
