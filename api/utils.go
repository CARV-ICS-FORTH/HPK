// Copyright © 2021 FORTH-ICS
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
// limitations under the License.package common

package api

import (
	"fmt"
	"strings"

	v1 "k8s.io/api/core/v1"
)

func NormalizeImageName(instance_name string) string {
	instances_str := strings.Split(string(instance_name), "/")
	final_name := ""
	first_iter := true
	for _, strings := range instances_str {
		if first_iter {
			final_name = strings
			first_iter = false
			continue
		}
		final_name = final_name + "-" + strings
	}
	without_version_stamp := strings.Split(final_name, ":")
	return without_version_stamp[0]
}

func BuildKeyFromNames(namespace string, name string) (string, error) {
	return fmt.Sprintf("%s-%s", namespace, name), nil
}

// buildKey is a helper for building the "key" for the providers pod store.
func BuildKey(pod *v1.Pod) (string, error) {
	if pod.ObjectMeta.Namespace == "" {
		return "", fmt.Errorf("pod namespace not found")
	}

	if pod.ObjectMeta.Name == "" {
		return "", fmt.Errorf("pod name not found")
	}

	return BuildKeyFromNames(pod.ObjectMeta.Namespace, pod.ObjectMeta.Name)
}
