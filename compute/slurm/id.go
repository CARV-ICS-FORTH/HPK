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
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

/************************************************************

		Parse Known Job Types

************************************************************/

type IDType string

const (
	IDTypeProcess IDType = "pid://"

	IDTypeSlurm IDType = "slurm://"

	IDTypeEmpty IDType = ""
)

func SetPodID(pod *corev1.Pod, idType IDType, value string) {
	metav1.SetMetaDataAnnotation(&pod.ObjectMeta, "pod.hpk/id", string(idType)+value)
}

func SetContainerStatusID(status *corev1.ContainerStatus, idType IDType, value string) {
	status.ContainerID = string(idType) + value
}

func ParsePodID(pod *corev1.Pod) (idType IDType, value string) {
	raw, exists := pod.GetAnnotations()["pod.hpk/id"]

	if !exists {
		return IDTypeEmpty, ""
	}

	return parseIDType(raw)
}

func ParseContainerID(status *corev1.ContainerStatus) (idType IDType, value string) {
	return parseIDType(status.ContainerID)
}

func parseIDType(raw string) (idType IDType, value string) {
	/*-- Extract id from raw format '<type>://<job_id>'. --*/
	switch {
	case strings.HasPrefix(raw, string(IDTypeSlurm)):
		return IDTypeSlurm, strings.Split(raw, string(IDTypeSlurm))[1]
	case strings.HasPrefix(raw, string(IDTypeProcess)):
		return IDTypeProcess, strings.Split(raw, string(IDTypeProcess))[1]
	default:
		panic("unknown id format: " + raw)
	}
}
