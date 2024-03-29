// Copyright © 2023 FORTH-ICS
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
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type JobIDType string

const (
	JobIDTypeInstance JobIDType = "instance://"

	JobIDTypeProcess JobIDType = "pid://"

	JobIDTypeSlurm JobIDType = "slurm://"

	JobIDTypeEmpty JobIDType = "Empty"
)

func SetPodID(pod *corev1.Pod, idType JobIDType, value string) {
	metav1.SetMetaDataAnnotation(&pod.ObjectMeta, "pod.hpk/id", string(idType)+value)
}

func SetContainerStatusID(status *corev1.ContainerStatus, typedValue string) {
	// ensure that the value follows an expected format.
	_ = parseIDType(typedValue)

	status.ContainerID = typedValue
}

func HasJobID(pod *corev1.Pod) bool {
	_, exists := pod.GetAnnotations()["pod.hpk/id"]

	return exists
}

func GetJobID(pod *corev1.Pod) string {
	raw, exists := pod.GetAnnotations()["pod.hpk/id"]

	if !exists {
		panic("this should not happen")
	}

	return parseIDType(raw)
}

func parseIDType(raw string) string {
	if strings.HasPrefix(raw, string(JobIDTypeSlurm)) {
		return strings.Split(raw, string(JobIDTypeSlurm))[1]
	}

	if strings.HasPrefix(raw, string(JobIDTypeProcess)) {
		return strings.Split(raw, string(JobIDTypeProcess))[1]
	}

	panic("unknown id format: " + raw)

	/*-- Extract id from raw format '<type>://<job_id>'.
	switch {

	case strings.HasPrefix(raw, string(JobIDTypeInstance)):
		return JobIDTypeInstance, strings.Split(raw, string(JobIDTypeInstance))[1]
	case strings.HasPrefix(raw, string(JobIDTypeProcess)):
		return JobIDTypeProcess, strings.Split(raw, string(JobIDTypeProcess))[1]
	default:

	}

	*/
}
