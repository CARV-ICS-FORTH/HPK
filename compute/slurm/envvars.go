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
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/carv-ics-forth/hpk/compute"
	"github.com/carv-ics-forth/hpk/pkg/crdtools"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// FromServices builds environment variables that a container is started with,
// which tell the container where to find the services it may need, which are
// provided as an argument.
func FromServices(services []*corev1.Service) []corev1.EnvVar {
	var result []corev1.EnvVar
	for _, service := range services {
		// ignore services where ClusterIP is "None" or empty
		// the services passed to this method should be pre-filtered
		// only services that have the cluster IP set should be included here
		// if !v1helper.IsServiceIPSet(service) {
		//	continue
		// }
		// if service.Spec.ClusterIP == "" || service.Spec.ClusterIP == "None" {
		//	continue
		// }

		// Host
		name := makeEnvVariableName(service.Name) + "_SERVICE_HOST"
		result = append(result, corev1.EnvVar{Name: name, Value: service.GetName() /*service.Spec.ClusterIP*/})
		// First port - give it the backwards-compatible name
		name = makeEnvVariableName(service.Name) + "_SERVICE_PORT"
		result = append(result, corev1.EnvVar{Name: name, Value: strconv.Itoa(int(service.Spec.Ports[0].Port))})
		// All named ports (only the first may be unnamed, checked in validation)
		for i := range service.Spec.Ports {
			sp := &service.Spec.Ports[i]
			if sp.Name != "" {
				pn := name + "_" + makeEnvVariableName(sp.Name)
				result = append(result, corev1.EnvVar{Name: pn, Value: strconv.Itoa(int(sp.Port))})
			}
		}
		// Docker-compatible vars.
		result = append(result, makeLinkVariables(service)...)
	}
	return result
}

func makeEnvVariableName(str string) string {
	// TODO: If we simplify to "all names are DNS1123Subdomains" this
	// will need two tweaks:
	//   1) Handle leading digits
	//   2) Handle dots
	return strings.ToUpper(strings.Replace(str, "-", "_", -1))
}

func makeLinkVariables(service *corev1.Service) []corev1.EnvVar {
	prefix := makeEnvVariableName(service.Name)
	all := []corev1.EnvVar{}
	for i := range service.Spec.Ports {
		sp := &service.Spec.Ports[i]

		protocol := string(corev1.ProtocolTCP)
		if sp.Protocol != "" {
			protocol = string(sp.Protocol)
		}

		// hostPort := net.JoinHostPort(service.Spec.ClusterIP, strconv.Itoa(int(sp.Port)))
		hostPort := net.JoinHostPort(service.GetName(), strconv.Itoa(int(sp.Port)))

		if i == 0 {
			// Docker special-cases the first port.
			all = append(all, corev1.EnvVar{
				Name:  prefix + "_PORT",
				Value: fmt.Sprintf("%s://%s", strings.ToLower(protocol), hostPort),
			})
		}
		portPrefix := fmt.Sprintf("%s_PORT_%d_%s", prefix, sp.Port, strings.ToUpper(protocol))
		all = append(all, []corev1.EnvVar{
			{
				Name:  portPrefix,
				Value: fmt.Sprintf("%s://%s", strings.ToLower(protocol), hostPort),
			},
			{
				Name:  portPrefix + "_PROTO",
				Value: strings.ToLower(protocol),
			},
			{
				Name:  portPrefix + "_PORT",
				Value: strconv.Itoa(int(sp.Port)),
			},
			{
				Name:  portPrefix + "_ADDR",
				Value: service.Spec.ClusterIP,
			},
		}...)
	}
	return all
}

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
