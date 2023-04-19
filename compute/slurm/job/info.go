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

package job

import (
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

/************************************************************

		Known Job Types

************************************************************/

type JobIDType string

const (
	JobIDTypeInstance JobIDType = "instance://"

	JobIDTypeProcess JobIDType = "pid://"

	JobIDTypeSlurm JobIDType = "slurm://"

	JobIDTypeEmpty JobIDType = ""
)

func SetPodID(pod *corev1.Pod, idType JobIDType, value string) {
	metav1.SetMetaDataAnnotation(&pod.ObjectMeta, "pod.hpk/id", string(idType)+value)
}

func SetContainerStatusID(status *corev1.ContainerStatus, typedValue string) {
	// ensure that the value follows an expected format.
	_, _ = parseIDType(typedValue)

	status.ContainerID = typedValue
}

func ParsePodID(pod *corev1.Pod) (idType JobIDType, value string) {
	raw, exists := pod.GetAnnotations()["pod.hpk/id"]

	if !exists {
		return JobIDTypeEmpty, ""
	}

	return parseIDType(raw)
}

func ParseContainerID(status *corev1.ContainerStatus) (idType JobIDType, value string) {
	return parseIDType(status.ContainerID)
}

func parseIDType(raw string) (idType JobIDType, value string) {
	/*-- Extract id from raw format '<type>://<job_id>'. --*/
	switch {
	case strings.HasPrefix(raw, string(JobIDTypeSlurm)):
		return JobIDTypeSlurm, strings.Split(raw, string(JobIDTypeSlurm))[1]
	case strings.HasPrefix(raw, string(JobIDTypeInstance)):
		return JobIDTypeInstance, strings.Split(raw, string(JobIDTypeInstance))[1]
	case strings.HasPrefix(raw, string(JobIDTypeProcess)):
		return JobIDTypeProcess, strings.Split(raw, string(JobIDTypeProcess))[1]
	default:
		panic("unknown id format: " + raw)
	}
}
