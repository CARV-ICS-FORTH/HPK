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
	"strings"

	"github.com/carv-ics-forth/hpk/compute"
	corev1 "k8s.io/api/core/v1"
	// discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// FromServices builds environment variables that a container is started with,
// which tell the container where to find the services it may need, which are
// provided as an argument.
func FromServices(ctx context.Context, namespace string) []corev1.EnvVar {
	/*---------------------------------------------------
	 * Get all Service resources from master
	 *---------------------------------------------------*/
	var serviceList corev1.ServiceList

	if err := compute.K8SClient.List(ctx, &serviceList, &client.ListOptions{
		LabelSelector: labels.Everything(),
	}); err != nil {
		compute.SystemPanic(err, "failed to list services when setting up env vars")
	}

	var services []*corev1.Service

	for i, service := range serviceList.Items {
		// We always want to add environment variable for master services
		// from the master service namespace, even if enableServiceLinks is false.
		// We also add environment variables for other services in the same
		// namespace, if enableServiceLinks is true.
		if service.GetNamespace() == namespace ||
			service.GetNamespace() == metav1.NamespaceDefault {
			services = append(services, &serviceList.Items[i])
		}
	}

	/*---------------------------------------------------
	 * Extract Environment Variables
	 *---------------------------------------------------*/
	var result []corev1.EnvVar
	for _, service := range services {
		// some headless services do not have ports.
		if len(service.Spec.Ports) == 0 {
			continue
		}

		// Host
		name := makeEnvVariableName(service.Name) + "_SERVICE_HOST"
		if service.GetNamespace() == metav1.NamespaceDefault && service.GetName() == "kubernetes" {
			// because kubernetes is not managed by HPK, we must create the entry manually.
			service.Spec.ClusterIP = compute.Environment.KubeMasterHost
		} else {
			// Look it up by DNS name.
			service.Spec.ClusterIP = service.GetName()
		}
		result = append(result, corev1.EnvVar{Name: name, Value: service.Spec.ClusterIP})

		// First port - give it the backwards-compatible name.
		name = makeEnvVariableName(service.Name) + "_SERVICE_PORT"
		result = append(result, corev1.EnvVar{Name: name, Value: service.Spec.Ports[0].TargetPort.String()})

		// All named ports (only the first may be unnamed, checked in validation).
		for i := range service.Spec.Ports {
			sp := &service.Spec.Ports[i]
			if sp.Name != "" {
				pn := name + "_" + makeEnvVariableName(sp.Name)
				result = append(result, corev1.EnvVar{Name: pn, Value: sp.TargetPort.String()})
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

		hostPort := net.JoinHostPort(service.Spec.ClusterIP, sp.TargetPort.String())

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
				Value: sp.TargetPort.String(),
			},
			{
				Name:  portPrefix + "_ADDR",
				Value: service.Spec.ClusterIP,
			},
		}...)
	}
	return all
}
