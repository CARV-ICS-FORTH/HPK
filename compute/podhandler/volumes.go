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

package podhandler

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/carv-ics-forth/hpk/compute/endpoint"
	"github.com/carv-ics-forth/hpk/compute/volume/configmap"
	"github.com/carv-ics-forth/hpk/compute/volume/emptydir"
	"github.com/carv-ics-forth/hpk/compute/volume/hostpath"
	"github.com/carv-ics-forth/hpk/compute/volume/projected"
	"github.com/carv-ics-forth/hpk/compute/volume/secret"
	"github.com/carv-ics-forth/hpk/compute/volume/util"
	"github.com/pkg/errors"
	mounter "k8s.io/utils/mount"

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

// mountVolumeSource prepares the volumes into the local pod directory.
// Critical errors related to the HPK fail directly.
// Misconfigurations (like wrong hostpaths), are returned as errors
func (h *PodHandler) mountVolumeSource(ctx context.Context, vol corev1.Volume) error {
	switch {
	case vol.VolumeSource.EmptyDir != nil:
		/*---------------------------------------------------
		 * EmptyDir
		 *---------------------------------------------------*/
		emptyDir := filepath.Join(h.podDirectory.VolumeDir(), vol.Name)

		if err := os.MkdirAll(emptyDir, endpoint.PodGlobalDirectoryPermissions); err != nil {
			compute.SystemPanic(err, "cannot create dir '%s'", emptyDir)
		}

		mounter := emptydir.VolumeMounter{
			Volume: vol,
			Pod:    *h.Pod,
			Logger: h.logger,
		}

		if err := mounter.SetUpAt(ctx, emptyDir); err != nil {
			compute.SystemPanic(err, "mount emptyDir volume to dir '%s' has failed", emptyDir)
		}

		h.logger.Info("  * EmptyDir Volume is mounted", "name", vol.Name)

		return nil

	case vol.VolumeSource.ConfigMap != nil:
		/*---------------------------------------------------
		 * ConfigMap
		 *---------------------------------------------------*/
		configMapDir := filepath.Join(h.podDirectory.VolumeDir(), vol.Name)

		if err := os.MkdirAll(configMapDir, endpoint.PodGlobalDirectoryPermissions); err != nil {
			compute.SystemPanic(err, "cannot create dir '%s'", configMapDir)
		}

		mounter := configmap.VolumeMounter{
			Volume: vol,
			Pod:    *h.Pod,
			Logger: h.logger,
		}

		if err := mounter.SetUpAt(ctx, configMapDir); err != nil {
			compute.DefaultLogger.Info("mount configMap volume has failed",
				"volume", vol.Name,
				"dir", configMapDir,
			)

			return errors.Wrapf(err, "failed to mount ConfigMap '%s'", vol.Name)
		}

		h.logger.Info("  * ConfigMap Volume is mounted", "name", vol.Name)

		return nil

	case vol.VolumeSource.Secret != nil:
		/*---------------------------------------------------
		 * Secret
		 *---------------------------------------------------*/
		secretDir := filepath.Join(h.podDirectory.VolumeDir(), vol.Name)

		if err := os.MkdirAll(secretDir, endpoint.PodGlobalDirectoryPermissions); err != nil {
			compute.SystemPanic(err, "cannot create dir '%s'", secretDir)
		}

		mounter := secret.VolumeMounter{
			Volume: vol,
			Pod:    *h.Pod,
			Logger: h.logger,
		}

		if err := mounter.SetUpAt(ctx, secretDir); err != nil {
			compute.DefaultLogger.Info("mount secret volume has failed",
				"volume", vol.Name,
				"dir", secretDir,
			)

			return errors.Wrapf(err, "failed to mount Secret '%s'", vol.Name)
		}

		h.logger.Info("  * Secret Volume is mounted", "name", vol.Name)

		return nil

	case vol.VolumeSource.DownwardAPI != nil:
		/*---------------------------------------------------
		 * Downward API
		 *---------------------------------------------------*/
		h.DownwardAPIVolumeSource(ctx, vol)

		h.logger.Info("  * DownwardAPI Volume is mounted", "name", vol.Name)

		return nil

	case vol.VolumeSource.HostPath != nil:
		/*---------------------------------------------------
		 * HostPath
		 *---------------------------------------------------*/
		if vol.VolumeSource.HostPath.Type == nil || *vol.VolumeSource.HostPath.Type == corev1.HostPathUnset {
			// ensure that the references host path exists
			exists, err := mounter.PathExists(vol.VolumeSource.HostPath.Path)
			if err != nil {
				compute.SystemPanic(err, "failed to inspect HostPath at path '%s'", vol.VolumeSource.HostPath.Path)
			}

			if !exists {
				return errors.Errorf("HostPath '%s' does not exist", vol.VolumeSource.HostPath.Path)
			}

			// Empty string (default) is for backward compatibility,
			// which means that no checks will be performed before mounting the hostPath volume.
			dstFullPath := filepath.Join(h.podDirectory.VolumeDir(), vol.Name)

			if err := os.Symlink(vol.VolumeSource.HostPath.Path, dstFullPath); err != nil {
				compute.SystemPanic(err, "cannot link symlink at path '%s'", dstFullPath)
			}

			// nothing to do
			return nil
		}

		mounter := hostpath.VolumeMounter{
			Volume: vol,
			Pod:    *h.Pod,
			Logger: h.logger,
		}

		if err := mounter.SetUpAt(ctx); err != nil {
			compute.SystemPanic(err, "mount hostpath volume has failed")
		}

		h.logger.Info("  * HostPath Volume is mounted", "name", vol.Name)

		return nil

	case vol.VolumeSource.PersistentVolumeClaim != nil:
		/*---------------------------------------------------
		 * Persistent Volume Claim
		 *---------------------------------------------------*/
		h.PersistentVolumeClaimSource(ctx, vol)

		h.logger.Info("  * PersistentVolumeClaim Volume is mounted", "name", vol.Name)

		return nil

	case vol.VolumeSource.Projected != nil:
		/*---------------------------------------------------
		 * Projected
		 *---------------------------------------------------*/
		projectedDir := filepath.Join(h.podDirectory.VolumeDir(), vol.Name)

		if err := os.MkdirAll(projectedDir, endpoint.PodGlobalDirectoryPermissions); err != nil {
			compute.SystemPanic(err, "cannot create dir '%s'", projectedDir)
		}

		mounter := projected.VolumeMounter{
			Volume: vol,
			Pod:    *h.Pod,
			Logger: h.logger,
		}

		if err := mounter.SetUpAt(ctx, projectedDir); err != nil {
			return errors.Wrapf(err, "failed to mount ProjectedVolume '%s'", vol.Name)
		}

		return nil

	default:
		logrus.Warn(vol)

		panic("It seems I have missed a Volume type")
	}
}

func (h *PodHandler) DownwardAPIVolumeSource(ctx context.Context, vol corev1.Volume) {
	downApiDir := filepath.Join(h.podDirectory.VolumeDir(), vol.Name)

	if err := os.MkdirAll(downApiDir, endpoint.PodGlobalDirectoryPermissions); err != nil {
		compute.SystemPanic(err, "cannot create dir '%s'", downApiDir)
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

func (h *PodHandler) PersistentVolumeClaimSource(ctx context.Context, vol corev1.Volume) {
	/*---------------------------------------------------
	 * Get the Referenced PVC from Volume
	 *---------------------------------------------------*/
	var pvc corev1.PersistentVolumeClaim
	{
		source := vol.VolumeSource.PersistentVolumeClaim

		key := types.NamespacedName{Namespace: h.Pod.GetNamespace(), Name: source.ClaimName}

		if errPVC := retry.OnError(NotFoundBackoff,
			func(err error) bool { // retry condition
				return k8errors.IsNotFound(err) || errors.Is(err, compute.ErrUnboundedPVC)
			},
			func() error { // execution
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
		); errPVC != nil { // error cehcking
			compute.PodError(h.Pod, "PVCError", "PVC (%s) has failed. error:'%W'", pvc.GetName(), errPVC)

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
			func(err error) bool { // retry condition
				return k8errors.IsNotFound(err)
			},
			func() error { // execution
				return compute.K8SClient.Get(ctx, key, &pv)
			},
		); errPV != nil { // error checking
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
	case pv.Spec.HostPath != nil:
		// dstFullPath := filepath.Join(h.podDirectory.VolumeDir(), vol.Name)

		// err := os.MkdirAll(pv.Spec.HostPath.Path, os.FileMode(0755))
		// if err != nil {
		// 	if !os.IsExist(err) {
		// 		compute.SystemPanic(err, "cannot create hostpath directory at path '%s'", pv.Spec.HostPath.Path)
		// 	}
		// }
		// if err := os.Symlink(pv.Spec.HostPath.Path, dstFullPath); err != nil {
		// 	compute.SystemPanic(err, "cannot link symlink at path '%s'", dstFullPath)
		// }

		// h.logger.Info("  * HostPath Volume is mounted", "fullpath", dstFullPath)
		panic("Hostpath pv")
	case pv.Spec.Local != nil:
		dstFullPath := filepath.Join(h.podDirectory.VolumeDir(), vol.Name)

		if err := os.Symlink(pv.Spec.Local.Path, dstFullPath); err != nil {
			compute.SystemPanic(err, "cannot link symlink at path '%s'", dstFullPath)
		}

		h.logger.Info("  * Local Volume is mounted", "fullpath", dstFullPath)
	default:
		logrus.Warn(vol)

		panic("It seems I have missed a PersistentVolume type")
	}
}
