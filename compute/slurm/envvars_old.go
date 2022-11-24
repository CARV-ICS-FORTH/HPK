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

	"github.com/carv-ics-forth/hpk/compute"
	"github.com/carv-ics-forth/hpk/pkg/crdtools"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// getServiceEnvVarMap makes a map[string]string of env vars for services a  pod in namespace ns should see.
// However, the kubelet implementation works on for Services with ClusterIPs.
// In our case, the Services point directly to Pod IP's, and the implementation does not work.
// The solution is to retrieve services from Endpoints.
//
// Original:
// https://github.com/kubernetes/kubernetes/blob/1139bb177b2b35611c5ca16cc82f0e41a8bb107e/pkg/kubelet/kubelet_pods.go#L575
func getServiceEnvVarMap(ctx context.Context, namespace string) ([]corev1.EnvVar, error) {
	/*---------------------------------------------------
	 * Get all service resources from master
	 *---------------------------------------------------*/
	var endpointsList corev1.EndpointsList

	if err := compute.K8SClient.List(ctx, &endpointsList, &client.ListOptions{
		LabelSelector: labels.Everything(),
	}); err != nil {
		return nil, errors.Wrap(err, "failed to list services when setting up env vars")
	}

	/*---------------------------------------------------
	 * Populate services into service environment variables.
	 *---------------------------------------------------*/
	var mappedEndpoints []*corev1.Endpoints

	for i, endpoint := range endpointsList.Items {
		// ignore endpoints without IPs
		if len(endpoint.Subsets) == 0 {
			logrus.Warn("Ignoring endpoint ", endpoint.GetName())

			continue
		}

		// We always want to add environment variabled for master services
		// from the master service namespace, even if enableServiceLinks is false.
		// We also add environment variables for other services in the same
		// namespace, if enableServiceLinks is true.
		if endpoint.GetNamespace() == namespace ||
			endpoint.GetNamespace() == metav1.NamespaceDefault {
			mappedEndpoints = append(mappedEndpoints, &endpointsList.Items[i])
		}
	}

	return crdtools.FromEndpoints(mappedEndpoints), nil
}
