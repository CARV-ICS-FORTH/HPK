/*
Copyright 2022 ICS-FORTH
Copyright 2014 The Kubernetes Authors.
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package crdtools

import (
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
)

// FromEndpoints builds environment variables that a container is started with,
// which tell the container where to find the services it may need, which are
// provided as an argument.
func FromEndpoints(endpoints []*corev1.Endpoints) []corev1.EnvVar {
	var result []corev1.EnvVar

	for _, endpoint := range endpoints {
		// fixme: always select the first endpoint
		subset := endpoint.Subsets[0]

		if subset.Addresses == nil {
			logrus.Warn("Ignoring subset ", subset.String())

			/*-- ignore addresses that are not ready --*/
			continue
		}

		// Host
		name := makeEnvVariableName(endpoint.Name) + "_SERVICE_HOST"
		result = append(result, corev1.EnvVar{Name: name, Value: subset.Addresses[0].IP})

		// First port - give it the backwards-compatible name
		name = makeEnvVariableName(endpoint.Name) + "_SERVICE_PORT"
		result = append(result, corev1.EnvVar{Name: name, Value: strconv.Itoa(int(subset.Ports[0].Port))})

		// All named ports (only the first may be unnamed, checked in validation)
		for _, port := range subset.Ports {
			if port.Name != "" {
				pn := name + "_" + makeEnvVariableName(port.Name)
				result = append(result, corev1.EnvVar{Name: pn, Value: strconv.Itoa(int(port.Port))})
			}
		}

		// Docker-compatible vars.
		result = append(result, makeLinkVariables(endpoint.Name, subset)...)
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

func makeLinkVariables(endpointName string, subset corev1.EndpointSubset) []corev1.EnvVar {
	var all []corev1.EnvVar

	prefix := makeEnvVariableName(endpointName)
	address := subset.Addresses[0].IP

	for i, port := range subset.Ports {
		protocol := string(corev1.ProtocolTCP)
		if port.Protocol != "" {
			protocol = string(port.Protocol)
		}

		hostPort := net.JoinHostPort(address, strconv.Itoa(int(port.Port)))

		if i == 0 {
			// Docker special-cases the first port.
			all = append(all, corev1.EnvVar{
				Name:  prefix + "_PORT",
				Value: fmt.Sprintf("%s://%s", strings.ToLower(protocol), hostPort),
			})
		}

		portPrefix := fmt.Sprintf("%s_PORT_%d_%s", prefix, port.Port, strings.ToUpper(protocol))
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
				Value: strconv.Itoa(int(port.Port)),
			},
			{
				Name:  portPrefix + "_ADDR",
				Value: address,
			},
		}...)
	}
	return all
}
