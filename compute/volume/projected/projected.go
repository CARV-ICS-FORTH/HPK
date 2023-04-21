// Copyright © 2022 FORTH-ICS
// Copyright 2015 The Kubernetes Authors.
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

package projected

import (
	"context"
	"fmt"

	"github.com/carv-ics-forth/hpk/compute"
	"github.com/carv-ics-forth/hpk/compute/volume"
	"github.com/carv-ics-forth/hpk/compute/volume/configmap"
	"github.com/carv-ics-forth/hpk/compute/volume/downwardapi"
	"github.com/carv-ics-forth/hpk/compute/volume/secret"
	"github.com/carv-ics-forth/hpk/compute/volume/util"
	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	k8errors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/client-go/util/retry"
)

// VolumeMounter handles retrieving secrets from the API server
// and placing them into the volume on the host.
type VolumeMounter struct {
	Volume corev1.Volume

	Pod corev1.Pod

	Logger logr.Logger
}

func (b *VolumeMounter) SetUpAt(ctx context.Context, dir string) error {
	podKey := types.NamespacedName{Namespace: b.Pod.GetNamespace(), Name: b.Pod.GetName()}

	if b.Volume.Projected.DefaultMode == nil {
		return errors.Errorf("no defaultMode used, not even the default value for it")
	}

	var data map[string]util.FileProjection
	var errCollect error

	if err := retry.OnError(volume.NotFoundBackoff, k8errors.IsNotFound,
		func() error {
			data, errCollect = b.collectData(ctx)
			return errCollect
		},
	); err != nil {
		return errors.Wrapf(err, "error preparing data for project volume. volume:'%s' pod:'%s'",
			b.Volume.Name, podKey)
	}

	/*---------------------------------------------------
	 * Mount Resource to the host
	 *---------------------------------------------------*/
	if err := util.MakeNestedMountpoints(b.Volume.Name, dir, b.Pod); err != nil {
		return err
	}

	writerContext := fmt.Sprintf("pod %s volume %v", podKey, b.Volume.Name)
	writer, err := util.NewAtomicWriter(dir, writerContext)
	if err != nil {
		return errors.Wrapf(err, "Error creating atomic writer")
	}

	err = writer.Write(data)
	if err != nil {
		return errors.Wrapf(err, "Error writing payload to dir")
	}

	// fixme: add permissions

	return nil
}

func (b *VolumeMounter) collectData(ctx context.Context) (map[string]util.FileProjection, error) {
	var errlist []error
	payload := make(map[string]util.FileProjection)

	for _, source := range b.Volume.Projected.Sources {
		switch {
		case source.Secret != nil:
			/*---------------------------------------------------
			 * Projected Secret
			 *---------------------------------------------------*/
			var secretAPI corev1.Secret

			optional := source.Secret.Optional != nil && *source.Secret.Optional
			key := types.NamespacedName{Namespace: b.Pod.GetNamespace(), Name: source.Secret.Name}

			{ // get the resource
				err := retry.OnError(volume.NotFoundBackoff, k8errors.IsNotFound,
					func() error {
						return compute.K8SClient.Get(ctx, key, &secretAPI)
					})
				if err != nil {
					if !(k8errors.IsNotFound(err) && optional) {
						b.Logger.Error(err, "Couldn't get projected.secret", "key", key)
						errlist = append(errlist, err)
						continue
					}

					secretAPI = corev1.Secret{
						ObjectMeta: metav1.ObjectMeta{
							Namespace: b.Pod.GetNamespace(),
							Name:      source.Secret.Name,
						},
					}
				}
			}

			secretPayload, err := secret.MakePayload(source.Secret.Items, &secretAPI, b.Volume.Projected.DefaultMode, optional)
			if err != nil {
				b.Logger.Error(err, "Couldn't get secret payload")
				errlist = append(errlist, err)
				continue
			}

			for k, v := range secretPayload {
				payload[k] = v
			}

		case source.ConfigMap != nil:
			/*---------------------------------------------------
			 * Projected ConfigMap
			 *---------------------------------------------------*/
			var configMapAPI corev1.ConfigMap

			optional := source.ConfigMap.Optional != nil && *source.ConfigMap.Optional
			key := types.NamespacedName{Namespace: b.Pod.GetNamespace(), Name: source.ConfigMap.Name}

			{ // get the resource
				err := retry.OnError(volume.NotFoundBackoff, k8errors.IsNotFound,
					func() error {
						return compute.K8SClient.Get(ctx, key, &configMapAPI)
					})
				if err != nil {
					if !(k8errors.IsNotFound(err) && optional) {
						b.Logger.Error(err, "Couldn't get projected.configmap", "key", key)
						errlist = append(errlist, err)
						continue
					}

					configMapAPI = corev1.ConfigMap{
						ObjectMeta: metav1.ObjectMeta{
							Namespace: b.Pod.GetNamespace(),
							Name:      source.ConfigMap.Name,
						},
					}
				}
			}

			configMapPayload, err := configmap.MakePayload(source.ConfigMap.Items, &configMapAPI, b.Volume.Projected.DefaultMode, optional)
			if err != nil {
				b.Logger.Error(err, "Couldn't get configMap payload")

				errlist = append(errlist, err)
				continue
			}

			for k, v := range configMapPayload {
				payload[k] = v
			}
		case source.DownwardAPI != nil:
			/*---------------------------------------------------
			 * Projected DownwardAPI
			 *---------------------------------------------------*/
			downwardAPIPayload, err := downwardapi.CollectData(source.DownwardAPI.Items, &b.Pod, b.Volume.Projected.DefaultMode)
			if err != nil {
				b.Logger.Error(err, "Couldn't get downwardAPI payload")

				errlist = append(errlist, err)
				continue
			}

			for k, v := range downwardAPIPayload {
				payload[k] = v
			}

		case source.ServiceAccountToken != nil:
			/*---------------------------------------------------
			 * Projected ServiceAccountToken
			 *---------------------------------------------------*/
			tokenRequest, err := compute.K8SClientset.CoreV1().
				ServiceAccounts(b.Pod.GetNamespace()).
				CreateToken(ctx, b.Pod.Spec.ServiceAccountName, &authenticationv1.TokenRequest{
					Spec: authenticationv1.TokenRequestSpec{
						Audiences: func() []string {
							if len(source.ServiceAccountToken.Audience) != 0 {
								return []string{source.ServiceAccountToken.Audience}
							}
							return nil
						}(),
						ExpirationSeconds: source.ServiceAccountToken.ExpirationSeconds,
						BoundObjectRef: &authenticationv1.BoundObjectReference{
							Kind:       "Pod",
							APIVersion: "v1",
							Name:       b.Pod.GetName(),
							UID:        b.Pod.GetUID(),
						},
					},
				}, metav1.CreateOptions{})
			if err != nil {
				errlist = append(errlist, err)
				continue
			}

			payload[source.ServiceAccountToken.Path] = util.FileProjection{
				Data: []byte(tokenRequest.Status.Token),
				Mode: *b.Volume.Projected.DefaultMode,
			}
		}
	}
	return payload, utilerrors.NewAggregate(errlist)
}
