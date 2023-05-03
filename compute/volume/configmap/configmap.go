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

// Package configmap contains the internal representation of configMap volumes.
package configmap

import (
	"context"
	"fmt"

	"github.com/carv-ics-forth/hpk/compute"
	"github.com/carv-ics-forth/hpk/compute/volume"
	"github.com/carv-ics-forth/hpk/compute/volume/util"
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

func (b *VolumeMounter) SetUpAt(ctx context.Context, dir string) error {
	var configMap corev1.ConfigMap

	source := b.Volume.ConfigMap
	optional := source.Optional != nil && *source.Optional

	/*---------------------------------------------------
	 * Get the Resource from Kubernetes
	 *---------------------------------------------------*/

	key := types.NamespacedName{Namespace: b.Pod.GetNamespace(), Name: source.Name}

	if err := retry.OnError(volume.NotFoundBackoff,
		k8errors.IsNotFound, // retry condition
		func() error { // execution
			return compute.K8SClient.Get(ctx, key, &configMap)
		}); err != nil { // error checking
		if !(k8errors.IsNotFound(err) && optional) {
			return errors.Wrapf(err, "Couldn't get secret '%s'", key)
		}

		configMap = corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: b.Pod.GetNamespace(),
				Name:      source.Name,
			},
		}
	}

	totalBytes := totalBytes(&configMap)

	b.Logger.Info("Received configMap",
		"configMap", source.Name,
		"data", len(configMap.Data)+len(configMap.BinaryData),
		"total", totalBytes,
	)

	/*---------------------------------------------------
	 * Mount Resource to the host
	 *---------------------------------------------------*/
	payload, err := MakePayload(source.Items, &configMap, source.DefaultMode, optional)
	if err != nil {
		return err
	}

	if err := util.MakeNestedMountpoints(b.Volume.Name, dir, b.Pod); err != nil {
		return err
	}

	// todo: Clean up directories if setup fails

	writerContext := fmt.Sprintf("Pod %v/%v volume %v", b.Pod.Namespace, b.Pod.Name, b.Volume.Name)

	writer, err := util.NewAtomicWriter(dir, writerContext)
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
func MakePayload(mappings []corev1.KeyToPath, configMap *corev1.ConfigMap, defaultMode *int32, optional bool) (map[string]util.FileProjection, error) {
	if defaultMode == nil {
		return nil, errors.Errorf("no defaultMode used, not even the default value for it")
	}

	payload := make(map[string]util.FileProjection, (len(configMap.Data) + len(configMap.BinaryData)))
	var fileProjection util.FileProjection

	if len(mappings) == 0 {
		for name, data := range configMap.Data {
			fileProjection.Data = []byte(data)
			fileProjection.Mode = *defaultMode
			payload[name] = fileProjection
		}
		for name, data := range configMap.BinaryData {
			fileProjection.Data = data
			fileProjection.Mode = *defaultMode
			payload[name] = fileProjection
		}
	} else {
		for _, ktp := range mappings {
			if stringData, ok := configMap.Data[ktp.Key]; ok {
				fileProjection.Data = []byte(stringData)
			} else if binaryData, ok := configMap.BinaryData[ktp.Key]; ok {
				fileProjection.Data = binaryData
			} else {
				if optional {
					continue
				}
				return nil, fmt.Errorf("configmap references non-existent config key: %s", ktp.Key)
			}

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

func totalBytes(configMap *corev1.ConfigMap) int {
	totalSize := 0
	for _, value := range configMap.Data {
		totalSize += len(value)
	}
	for _, value := range configMap.BinaryData {
		totalSize += len(value)
	}

	return totalSize
}
