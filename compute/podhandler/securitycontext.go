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

package podhandler

import (
	corev1 "k8s.io/api/core/v1"
)

const (
	RootUID   = 0
	RootGID   = 0
	NobodyUID = 65534
	NobodyGID = 65534
)

// DetermineEffectiveSecurityContext returns a synthesized SecurityContext for reading effective configurations
// from the provided pod's and container's security context. Container's fields take precedence in cases where both
// are set
func DetermineEffectiveSecurityContext(pod *corev1.Pod, container *corev1.Container) *corev1.SecurityContext {
	effectiveSc := securityContextFromPodSecurityContext(pod)
	containerSc := container.SecurityContext

	if effectiveSc == nil && containerSc == nil {
		return &corev1.SecurityContext{}
	}
	if effectiveSc != nil && containerSc == nil {
		return effectiveSc
	}
	if effectiveSc == nil && containerSc != nil {
		return containerSc
	}

	if containerSc.SELinuxOptions != nil {
		effectiveSc.SELinuxOptions = new(corev1.SELinuxOptions)
		*effectiveSc.SELinuxOptions = *containerSc.SELinuxOptions
	}

	if containerSc.WindowsOptions != nil {
		// only override fields that are set at the container level, not the whole thing
		if effectiveSc.WindowsOptions == nil {
			effectiveSc.WindowsOptions = &corev1.WindowsSecurityContextOptions{}
		}
		if containerSc.WindowsOptions.GMSACredentialSpecName != nil || containerSc.WindowsOptions.GMSACredentialSpec != nil {
			// both GMSA fields go hand in hand
			effectiveSc.WindowsOptions.GMSACredentialSpecName = containerSc.WindowsOptions.GMSACredentialSpecName
			effectiveSc.WindowsOptions.GMSACredentialSpec = containerSc.WindowsOptions.GMSACredentialSpec
		}
		if containerSc.WindowsOptions.RunAsUserName != nil {
			effectiveSc.WindowsOptions.RunAsUserName = containerSc.WindowsOptions.RunAsUserName
		}
		if containerSc.WindowsOptions.HostProcess != nil {
			effectiveSc.WindowsOptions.HostProcess = containerSc.WindowsOptions.HostProcess
		}
	}

	if containerSc.Capabilities != nil {
		effectiveSc.Capabilities = new(corev1.Capabilities)
		*effectiveSc.Capabilities = *containerSc.Capabilities
	}

	if containerSc.Privileged != nil {
		effectiveSc.Privileged = new(bool)
		*effectiveSc.Privileged = *containerSc.Privileged
	}

	if containerSc.RunAsNonRoot != nil {
		effectiveSc.RunAsNonRoot = new(bool)
		*effectiveSc.RunAsNonRoot = *containerSc.RunAsNonRoot
	}

	if containerSc.RunAsUser != nil {
		effectiveSc.RunAsUser = new(int64)
		*effectiveSc.RunAsUser = *containerSc.RunAsUser
	}

	if containerSc.RunAsGroup != nil {
		effectiveSc.RunAsGroup = new(int64)
		*effectiveSc.RunAsGroup = *containerSc.RunAsGroup
	}

	if containerSc.ReadOnlyRootFilesystem != nil {
		effectiveSc.ReadOnlyRootFilesystem = new(bool)
		*effectiveSc.ReadOnlyRootFilesystem = *containerSc.ReadOnlyRootFilesystem
	}

	if containerSc.AllowPrivilegeEscalation != nil {
		effectiveSc.AllowPrivilegeEscalation = new(bool)
		*effectiveSc.AllowPrivilegeEscalation = *containerSc.AllowPrivilegeEscalation
	}

	if containerSc.ProcMount != nil {
		effectiveSc.ProcMount = new(corev1.ProcMountType)
		*effectiveSc.ProcMount = *containerSc.ProcMount
	}

	return effectiveSc
}

func securityContextFromPodSecurityContext(pod *corev1.Pod) *corev1.SecurityContext {
	if pod.Spec.SecurityContext == nil {
		return nil
	}

	synthesized := &corev1.SecurityContext{}

	if pod.Spec.SecurityContext.SELinuxOptions != nil {
		synthesized.SELinuxOptions = &corev1.SELinuxOptions{}
		*synthesized.SELinuxOptions = *pod.Spec.SecurityContext.SELinuxOptions
	}

	if pod.Spec.SecurityContext.WindowsOptions != nil {
		synthesized.WindowsOptions = &corev1.WindowsSecurityContextOptions{}
		*synthesized.WindowsOptions = *pod.Spec.SecurityContext.WindowsOptions
	}

	if pod.Spec.SecurityContext.RunAsUser != nil {
		synthesized.RunAsUser = new(int64)
		*synthesized.RunAsUser = *pod.Spec.SecurityContext.RunAsUser
	}

	if pod.Spec.SecurityContext.RunAsGroup != nil {
		synthesized.RunAsGroup = new(int64)
		*synthesized.RunAsGroup = *pod.Spec.SecurityContext.RunAsGroup
	}

	if pod.Spec.SecurityContext.RunAsNonRoot != nil {
		synthesized.RunAsNonRoot = new(bool)
		*synthesized.RunAsNonRoot = *pod.Spec.SecurityContext.RunAsNonRoot
	}

	return synthesized
}

func DetermineEffectiveRunAsUser(sc *corev1.SecurityContext) (uid int64, gid int64) {
	uid = RootUID
	gid = RootGID

	// Change to "nobody:nobody" if RunAsNonRoot is specified, but not the specific user.
	if sc.RunAsNonRoot != nil && *sc.RunAsNonRoot {
		uid = NobodyUID
		gid = NobodyGID
	}

	if sc.RunAsUser != nil {
		uid = *sc.RunAsUser
	}

	if sc.RunAsGroup != nil {
		gid = *sc.RunAsGroup
	}

	return uid, gid
}
