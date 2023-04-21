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

	"github.com/carv-ics-forth/hpk/compute/volume/configmap"
	"github.com/carv-ics-forth/hpk/compute/volume/emptydir"
	"github.com/carv-ics-forth/hpk/compute/volume/hostpath"
	"github.com/carv-ics-forth/hpk/compute/volume/projected"
	"github.com/carv-ics-forth/hpk/compute/volume/secret"
	"github.com/carv-ics-forth/hpk/compute/volume/util"
	"github.com/pkg/errors"

	"github.com/carv-ics-forth/hpk/compute"
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
	Steps:    10,
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
		emptyDir, err := h.podDirectory.CreateVolume(vol.Name, compute.PodGlobalDirectoryPermissions)
		if err != nil {
			compute.SystemPanic(err, "cannot create dir '%s' for emptyDir", emptyDir)
		}

		mounter := emptydir.VolumeMounter{
			Volume: vol,
			Pod:    *h.Pod,
			Logger: h.logger,
		}

		if err = mounter.SetUpAt(ctx, emptyDir); err != nil {
			compute.SystemPanic(err, "mount emptyDir volume to dir '%s' has failed", emptyDir)
		}

		h.logger.Info("  * EmptyDir Volume is mounted", "name", vol.Name)

	case vol.VolumeSource.ConfigMap != nil:
		/*---------------------------------------------------
		 * ConfigMap
		 *---------------------------------------------------*/
		configMapDir, err := h.podDirectory.CreateVolume(vol.Name, compute.PodGlobalDirectoryPermissions)
		if err != nil {
			compute.SystemPanic(err, "cannot create dir '%s' for configmap", configMapDir)
		}

		mounter := configmap.VolumeMounter{
			Volume: vol,
			Pod:    *h.Pod,
			Logger: h.logger,
		}

		if err = mounter.SetUpAt(ctx, configMapDir); err != nil {
			compute.SystemPanic(err, "mount configmap volume to dir '%s' has failed", configMapDir)
		}

		h.logger.Info("  * ConfigMap Volume is mounted", "name", vol.Name)

	case vol.VolumeSource.Secret != nil:
		/*---------------------------------------------------
		 * Secret
		 *---------------------------------------------------*/
		secretDir, err := h.podDirectory.CreateVolume(vol.Name, compute.PodGlobalDirectoryPermissions)
		if err != nil {
			compute.SystemPanic(err, "cannot create dir '%s' for secrets", secretDir)
		}

		mounter := secret.VolumeMounter{
			Volume: vol,
			Pod:    *h.Pod,
			Logger: h.logger,
		}

		if err = mounter.SetUpAt(ctx, secretDir); err != nil {
			compute.SystemPanic(err, "mount secret volume to dir '%s' has failed", secretDir)
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
			if path, err := h.podDirectory.CreateVolumeLink(vol.VolumeSource.HostPath.Path, vol.Name); err != nil {
				compute.SystemPanic(err, "cannot create HostPathDirectoryOrCreate at path '%s'", path)
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
			compute.SystemPanic(err, "mount hostpath volum has failed")
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
		projectedDir, err := h.podDirectory.CreateVolume(vol.Name, compute.PodGlobalDirectoryPermissions)
		if err != nil {
			compute.SystemPanic(err, "cannot create dir '%s' for projected volumes", projectedDir)
		}

		mounter := projected.VolumeMounter{
			Volume: vol,
			Pod:    *h.Pod,
			Logger: h.logger,
		}

		if err = mounter.SetUpAt(ctx, projectedDir); err != nil {
			compute.SystemPanic(err, "mount projected volume to dir '%s' has failed", projectedDir)
		}

		h.logger.Info("  * Projected Volume is mounted", "name", vol.Name)

	default:
		logrus.Warn(vol)

		panic("It seems I have missed a Volume type")
	}
}

func (h *podHandler) DownwardAPIVolumeSource(ctx context.Context, vol corev1.Volume) {
	downApiDir, err := h.podDirectory.CreateVolume(vol.Name, compute.PodGlobalDirectoryPermissions)
	if err != nil {
		compute.SystemPanic(err, "cannot create dir '%s' for downwardApi", downApiDir)
	}

	for _, item := range vol.DownwardAPI.Items {
		itemPath := filepath.Join(downApiDir, item.Path)
		value, err := fieldpath.ExtractFieldPathAsString(h.Pod, item.FieldRef.FieldPath)
		if err != nil {
			compute.PodError(h.Pod, compute.ReasonSpecError, err.Error())

			return
		}

		if err := os.WriteFile(itemPath, []byte(value), fs.FileMode(*vol.Projected.DefaultMode)); err != nil {
			compute.SystemPanic(err, "cannot write config map file '%s'", itemPath)
		}
	}
}

func (h *podHandler) PersistentVolumeClaimSource(ctx context.Context, vol corev1.Volume) {
	/*---------------------------------------------------
	 * Get the Referenced PVC from Volume
	 *---------------------------------------------------*/
	var pvc corev1.PersistentVolumeClaim
	{
		source := vol.VolumeSource.PersistentVolumeClaim

		key := types.NamespacedName{Namespace: h.Pod.GetNamespace(), Name: source.ClaimName}

		if errPVC := retry.OnError(NotFoundBackoff,
			// retry condition
			func(err error) bool {
				return k8errors.IsNotFound(err) || errors.Is(err, compute.ErrUnboundedPVC)
			},
			// execution
			func() error {
				if err := compute.K8SClient.Get(ctx, key, &pvc); err != nil {
					compute.DefaultLogger.Info("Failed to get PVC", "pvcName", pvc.GetName())

					return err
				}

				// filter-out unsupported pvc
				if util.CheckPersistentVolumeClaimModeBlock(&pvc) {
					compute.DefaultLogger.Info("Unsupported PVC mode", "pvcName", pvc.GetName())

					return compute.ErrUnsupportedClaimMode
				}

				// ensure that pvc is bounded to a pv
				if pvc.Status.Phase != corev1.ClaimBound {
					compute.DefaultLogger.Info("Waiting for PVC to become bounded to a PV",
						"pvcName", pvc.GetName(),
					)

					return compute.ErrUnboundedPVC
				}

				return nil
			},
		); errPVC != nil {
			compute.PodError(h.Pod, "PVCError", "PVC (%s) has failed. error:'%W'",
				pvc.GetName(),
				errPVC,
			)

			return
		}
	}

	/*---------------------------------------------------
	 * Get the referenced PV from PVC
	 *---------------------------------------------------*/
	var pv corev1.PersistentVolume
	{
		key := types.NamespacedName{Namespace: h.Pod.GetNamespace(), Name: pvc.Spec.VolumeName}

		if errPV := retry.OnError(NotFoundBackoff,
			// retry condition
			func(err error) bool {
				return k8errors.IsNotFound(err)
			},
			// execution
			func() error {
				return compute.K8SClient.Get(ctx, key, &pv)
			},
		); errPV != nil {
			compute.PodError(h.Pod, "PVError", "PV (%s) has failed. err:'%W'",
				pv.GetName(),
				errPV,
			)
		}
	}

	h.logger.Info("PVC bounding info", "pvc", pvc.Spec, "pv", pv.Spec)

	/*---------------------------------------------------
	 * Link the Referenced PV to the Pod's Volumes
	 *---------------------------------------------------*/
	switch {
	case pv.Spec.Local != nil:
		if path, err := h.podDirectory.CreateVolumeLink(pv.Spec.Local.Path, vol.Name); err != nil {
			compute.SystemPanic(err, "cannot create local pv at path '%s'", path)
		}

		h.logger.Info("  * Local Volume is mounted", "name", vol.Name)
	default:
		logrus.Warn(vol)

		panic("It seems I have missed a PersistentVolume type")
	}
}
