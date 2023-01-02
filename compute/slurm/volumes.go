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

	"github.com/carv-ics-forth/hpk/compute/slurm/volume/projected"
	"github.com/carv-ics-forth/hpk/compute/slurm/volume/util"

	"github.com/carv-ics-forth/hpk/compute/slurm/volume/configmap"
	"github.com/carv-ics-forth/hpk/compute/slurm/volume/hostpath"

	"github.com/carv-ics-forth/hpk/compute"
	"github.com/carv-ics-forth/hpk/compute/slurm/volume/secret"
	"github.com/carv-ics-forth/hpk/pkg/fieldpath"
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

func (h *podHandler) mountVolumeSource(ctx context.Context, vol corev1.Volume) {
	switch {
	case vol.VolumeSource.EmptyDir != nil:
		/*---------------------------------------------------
		 * EmptyDir
		 *---------------------------------------------------*/
		emptyDir, err := h.podDirectory.CreateSubDirectory(vol.Name, compute.PodGlobalDirectoryPermissions)
		if err != nil {
			SystemError(err, "cannot create dir '%s' for emptyDir", emptyDir)
		}
		// without size limit for now

		h.logger.Info("  * EmptyDir Volume is mounted", "name", vol.Name)

	case vol.VolumeSource.ConfigMap != nil:
		/*---------------------------------------------------
		 * ConfigMap
		 *---------------------------------------------------*/
		mounter := configmap.VolumeMounter{
			Volume: vol,
			Pod:    *h.Pod,
			Logger: h.logger,
		}

		// .hpk/namespace/podName/.virtualenv/secretName/*
		podConfigMapDir, err := h.podDirectory.CreateSubDirectory(vol.Name, compute.PodGlobalDirectoryPermissions)
		if err != nil {
			SystemError(err, "cannot create dir '%s' for configmap", podConfigMapDir)
		}

		if err = mounter.SetUpAt(ctx, podConfigMapDir); err != nil {
			SystemError(err, "mount secret volume to dir '%s' has failed", podConfigMapDir)
		}

		h.logger.Info("  * ConfigMap Volume is mounted", "name", vol.Name)

	case vol.VolumeSource.Secret != nil:
		/*---------------------------------------------------
		 * Secret
		 *---------------------------------------------------*/
		mounter := secret.VolumeMounter{
			Volume: vol,
			Pod:    *h.Pod,
			Logger: h.logger,
		}

		// .hpk/namespace/podName/.virtualenv/secretName/*
		podSecretDir, err := h.podDirectory.CreateSubDirectory(vol.Name, compute.PodGlobalDirectoryPermissions)
		if err != nil {
			SystemError(err, "cannot create dir '%s' for secrets", podSecretDir)
		}

		if err = mounter.SetUpAt(ctx, podSecretDir); err != nil {
			SystemError(err, "mount secret volume to dir '%s' has failed", podSecretDir)
		}

		h.logger.Info("  * Secret Volume is mounted", "name", vol.Name)

	case vol.VolumeSource.DownwardAPI != nil:
		/*---------------------------------------------------
		 * Downward API
		 *---------------------------------------------------*/
		h.DownwardAPIVolumeSource(ctx, vol)

		h.logger.Info("  * DownwardAPI Volume is mounted", "name", vol.Name)

	case vol.VolumeSource.HostPath != nil:
		/*---------------------------------------------------
		 * HostPath
		 *---------------------------------------------------*/
		if vol.VolumeSource.HostPath.Type == nil || *vol.VolumeSource.HostPath.Type == corev1.HostPathUnset {
			// For backwards compatible, leave it empty if unset
			if path, err := h.podDirectory.CreateSymlink(vol.VolumeSource.HostPath.Path, vol.Name); err != nil {
				SystemError(err, "cannot create HostPathDirectoryOrCreate at path '%s'", path)
			}

			h.logger.Info("  * HostPath Volume is mounted", "name", vol.Name)

			break
		}

		mounter := hostpath.VolumeMounter{
			Volume: vol,
			Pod:    *h.Pod,
			Logger: h.logger,
		}

		if err := mounter.SetUpAt(ctx); err != nil {
			SystemError(err, "mount hostpath volum has failed")
		}

		h.logger.Info("  * HostPath Volume is mounted", "name", vol.Name)

	case vol.VolumeSource.PersistentVolumeClaim != nil:
		/*---------------------------------------------------
		 * Persistent Volume Claim
		 *---------------------------------------------------*/
		h.PersistentVolumeClaimSource(ctx, vol)

		h.logger.Info("  * PersistentVolumeClaim Volume is mounted", "name", vol.Name)

	case vol.VolumeSource.Projected != nil:
		/*---------------------------------------------------
		 * Projected
		 *---------------------------------------------------*/
		mounter := projected.VolumeMounter{
			Volume: vol,
			Pod:    *h.Pod,
			Logger: h.logger,
		}

		// .hpk/namespace/podName/.virtualenv/projected/*
		projectedDir, err := h.podDirectory.CreateSubDirectory(vol.Name, compute.PodGlobalDirectoryPermissions)
		if err != nil {
			SystemError(err, "cannot create dir '%s' for projected volumes", projectedDir)
		}

		if err = mounter.SetUpAt(ctx, projectedDir); err != nil {
			SystemError(err, "mount projected volume to dir '%s' has failed", projectedDir)
		}

		h.logger.Info("  * Projected Volume is mounted", "name", vol.Name)

	default:
		logrus.Warn(vol)

		panic("It seems I have missed a Volume type")
	}
}

func (h *podHandler) DownwardAPIVolumeSource(ctx context.Context, vol corev1.Volume) {
	downApiDir, err := h.podDirectory.CreateSubDirectory(vol.Name, compute.PodGlobalDirectoryPermissions)
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

func (h *podHandler) PersistentVolumeClaimSource(ctx context.Context, vol corev1.Volume) {
	var pvc corev1.PersistentVolumeClaim

	{ // get pvc
		source := vol.VolumeSource.PersistentVolumeClaim

		key := types.NamespacedName{Namespace: h.Pod.GetNamespace(), Name: source.ClaimName}

		err := retry.OnError(NotFoundBackoff, k8errors.IsNotFound,
			func() error {
				return compute.K8SClient.Get(ctx, key, &pvc)
			})
		if err != nil {
			if k8errors.IsNotFound(err) {
				PodError(h.Pod, "PVCNotFound", "cannot find persistentVolumeClaim '%s'", key)

				return
			}
			SystemError(err, "error getting persistentVolumeClaim '%s'", key)
		}
	}

	{ // filter-out unsupported pvc
		if util.CheckPersistentVolumeClaimModeBlock(&pvc) {
			PodError(h.Pod, "UnsupportedMode", "hpk does not support block volume provisioning")

			return
		}

		if pvc.Status.Phase != corev1.ClaimBound {
			// fixme: should we retry until it becomes bound?
			PodError(h.Pod, "PVCNotBound", "pvc '%s' is not yet bounded to a pv")

			return
		}
	}

	var pv corev1.PersistentVolume
	{ // get bounded pv
		key := types.NamespacedName{Namespace: h.Pod.GetNamespace(), Name: pvc.Spec.VolumeName}

		err := retry.OnError(NotFoundBackoff, k8errors.IsNotFound,
			func() error {
				return compute.K8SClient.Get(ctx, key, &pv)
			})
		if err != nil {
			if k8errors.IsNotFound(err) {
				PodError(h.Pod, "PVNotFound", "cannot find bounded persistentVolume '%s'", key)

				return
			}
			SystemError(err, "error getting bounded persistentVolume '%s'", key)
		}
	}

	h.logger.Info("PVC bounding info", "pvc", pvc.Spec, "pv", pv.Spec)

	// mount the references PV
	switch {
	case pv.Spec.Local != nil:
		/*---------------------------------------------------
		 * Local
		 *---------------------------------------------------*/
		if path, err := h.podDirectory.CreateSymlink(pv.Spec.Local.Path, vol.Name); err != nil {
			SystemError(err, "cannot create local pv at path '%s'", path)
		}

		h.logger.Info("  * Local Volume is mounted", "name", vol.Name)
	default:
		logrus.Warn(vol)

		panic("It seems I have missed a PersistentVolume type")
	}
}

