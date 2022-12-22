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

// Package secret contains the internal representation of secret volumes.
package secret

import (
	"context"
	"fmt"

	"github.com/carv-ics-forth/hpk/compute"
	"github.com/carv-ics-forth/hpk/compute/slurm/volume"
	volumeutil "github.com/carv-ics-forth/hpk/compute/slurm/volume/util"
	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	k8errors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
)

// VolumeMounter handles retrieving secrets from the API server
// and placing them into the volume on the host.
type VolumeMounter struct {
	Volume corev1.Volume
	Pod    corev1.Pod

	Logger logr.Logger
}

func (b *VolumeMounter) SetUpAt(ctx context.Context, dir string, mounterArgs volume.MounterArgs) error {
	b.Logger.Info("Setting up volume for Pod", "volume", b.Volume.Name, "dir", dir)

	var secret corev1.Secret

	source := b.Volume.Secret
	optional := source.Optional != nil && *source.Optional

	/*---------------------------------------------------
	 * Get the Resource from Kubernetes
	 *---------------------------------------------------*/

	key := types.NamespacedName{Namespace: b.Pod.GetNamespace(), Name: source.SecretName}

	err := retry.OnError(volume.NotFoundBackoff, k8errors.IsNotFound,
		func() error {
			return compute.K8SClient.Get(ctx, key, &secret)
		})

	if err != nil {
		if !(k8errors.IsNotFound(err) && optional) {
			return errors.Wrapf(err, "Couldn't get secret '%s'", key)
		}

		secret = corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: b.Pod.GetNamespace(),
				Name:      source.SecretName,
			},
		}
	}

	totalBytes := totalSecretBytes(&secret)

	b.Logger.Info("Received secret",
		"secret", source.SecretName,
		"data", len(secret.Data),
		"total", totalBytes,
	)

	/*---------------------------------------------------
	 * Mount Resource to the host
	 *---------------------------------------------------*/
	payload, err := MakePayload(source.Items, &secret, source.DefaultMode, optional)
	if err != nil {
		return err
	}

	if err := volumeutil.MakeNestedMountpoints(b.Volume.Name, dir, b.Pod); err != nil {
		return err
	}

	writerContext := fmt.Sprintf("Pod %v/%v volume %v", b.Pod.Namespace, b.Pod.Name, b.Volume.Name)

	writer, err := volumeutil.NewAtomicWriter(dir, writerContext)
	if err != nil {
		return errors.Wrapf(err, "Error creating atomic writer")
	}

	if err := writer.Write(payload); err != nil {
		return errors.Wrapf(err, "Error writing payload to dir")
	}

	// fixme: add permissions

	return nil
}

// MakePayload function is exported so that it can be called from the projection volume driver
func MakePayload(mappings []corev1.KeyToPath, secret *corev1.Secret, defaultMode *int32, optional bool) (map[string]volumeutil.FileProjection, error) {
	if defaultMode == nil {
		return nil, errors.Errorf("no defaultMode used, not even the default value for it")
	}

	payload := make(map[string]volumeutil.FileProjection, len(secret.Data))
	var fileProjection volumeutil.FileProjection

	if len(mappings) == 0 {
		for name, data := range secret.Data {
			fileProjection.Data = data
			fileProjection.Mode = *defaultMode
			payload[name] = fileProjection
		}
	} else {
		for _, ktp := range mappings {
			content, ok := secret.Data[ktp.Key]
			if !ok {
				if optional {
					continue
				}

				return nil, errors.Errorf("references non-existent secret key: %s", ktp.Key)
			}

			fileProjection.Data = content
			if ktp.Mode != nil {
				fileProjection.Mode = *ktp.Mode
			} else {
				fileProjection.Mode = *defaultMode
			}
			payload[ktp.Path] = fileProjection
		}
	}

	return payload, nil
}

func totalSecretBytes(secret *corev1.Secret) int {
	totalSize := 0
	for _, bytes := range secret.Data {
		totalSize += len(bytes)
	}

	return totalSize
}
