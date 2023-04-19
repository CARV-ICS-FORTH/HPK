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

package compute

import (
	"fmt"
	"regexp"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
)

var escapeScripts = regexp.MustCompile(`(--[A-Za-z0-9\-]+=)(\$[{\(][A-Za-z0-9_]+[\)\}])`)

var (
	ReasonObjectNotFound      = "ObjectNotFound"
	ReasonSpecError           = "SpecError"
	ReasonUnsupportedFeatures = "UnsupportedFeatures"
	ReasonExecutionError      = "ExecutionError"
	ReasonInitializationError = "InitializationError"
)

// Volume Errors
var (
	ErrUnboundedPVC         = errors.New("unbound pvc")
	ErrUnsupportedClaimMode = errors.New("hpk does not support block volume provisioning")
)

func PodError(pod *corev1.Pod, reason string, msgFormat string, msgArgs ...any) {
	pod.Status.Phase = corev1.PodFailed
	pod.Status.Reason = reason
	pod.Status.Message = fmt.Sprintf(msgFormat, msgArgs...)
}

func SystemError(err error, errFormat string, errArgs ...any) {
	werr := errors.Wrapf(err, errFormat, errArgs...)

	DefaultLogger.Error(werr, "SystemERROR")

	panic(werr)
}
