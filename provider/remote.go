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
// limitations under the License.package main

package provider

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"github.com/carv-ics-forth/knoc/api"
	"github.com/pkg/errors"
	"github.com/sfreiberg/simplessh"
	"github.com/sirupsen/logrus"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var SSHClient *simplessh.Client

func init() {
	client, err := simplessh.ConnectWithKey(os.Getenv("REMOTE_HOST")+":"+os.Getenv("REMOTE_PORT"), os.Getenv("REMOTE_USER"), os.Getenv("REMOTE_KEY"))
	if err != nil {
		panic(err)
	}

	SSHClient = client

	if err := prepareDoor(); err != nil {
		panic(err)
	}
}

func prepareDoor() error {
	if _, err := SSHClient.Exec("./door --version"); err != nil {
		return errors.Wrapf(err, "door validation error")
	}

	// Could not find KNoC's Door binary in the remote system...
	// send door to remote
	local := "/usr/local/bin/door" // from inside the container's root dir
	remote := "door"

	if err := UploadFile(local, remote, 0700); err != nil {
		return errors.Wrapf(err, "upload file")
	}

	// check again else die
	if _, err := SSHClient.Exec("./door --version"); err != nil {
		return errors.Wrapf(err, "Could not upload KNoC's Door")
	}

	return nil
}

func RemoteExecution(p *Provider, ctx context.Context, mode api.Operation, pod *corev1.Pod) error {
	encoded, err := json.Marshal(pod)
	if err != nil {
		return errors.Wrapf(err, "cannot marshal pod '%s'", pod.GetName())
	}

	switch mode {
	case api.SUBMIT:
		if err := PrepareExecutionEnvironment(p, ctx, pod); err != nil {
			return errors.Wrap(err, "prepare container data")
		}

		cmd := "bash -l -c \"nohup ./door -a submit -c " + string(encoded) + " -V >> .knoc/door.log 2>> .knoc/door.log < /dev/null & \""
		logrus.Debugf(cmd)

		output, err := SSHClient.Exec(cmd)
		if err != nil {
			return errors.Wrapf(err, "Cannot submit job")
		}

		logrus.Warn("Submit result ", string(output))

	case api.DELETE:
		cmd := "bash -l -c \"nohup ./door -a stop -c " + string(encoded) + " -V >> .knoc/door.log 2>> .knoc/door.log < /dev/null & \""
		logrus.Debugln(cmd)

		output, err := SSHClient.Exec(cmd)
		if err != nil {
			return errors.Wrapf(err, "Cannot delete job")
		}

		logrus.Warn("Submit result ", string(output))
	}

	return nil
}

func PrepareExecutionEnvironment(p *Provider, ctx context.Context, pod *corev1.Pod) error {
	log.G(ctx).Debugf("receive prepareContainerData %v", pod.GetName())

	/*
		add kubeconfig on remote:$HOME
	*/
	out, err := exec.Command("test -f .kube/config").Output()
	if _, ok := err.(*exec.ExitError); !ok {
		log.GetLogger(ctx).Debug("Kubeconfig doesn't exist, so we will generate it...")

		out, err = exec.Command("/bin/sh", "/home/user0/scripts/prepare_kubeconfig.sh").Output()
		if err != nil {
			return errors.Wrapf(err, "Could not run kubeconfig_setup script!")
		}

		log.GetLogger(ctx).Debug("Kubeconfig generated")

		if _, err := SSHClient.Exec("mkdir -p .kube"); err != nil {
			return errors.Wrapf(err, "cannot create dir")
		}

		if _, err := SSHClient.Exec("echo \"" + string(out) + "\" > .kube/config"); err != nil {
			return errors.Wrapf(err, "Could not setup kubeconfig on the remote system ")
		}

		log.GetLogger(ctx).Debug("Kubeconfig installed")
	}

	/*
		.knoc is used for runtime files
	*/
	if _, err := SSHClient.Exec("mkdir -p .knoc"); err != nil {
		return errors.Wrapf(err, "cannot create .knoc")
	}

	/*
		Copy volumes from local Pod to remote Environment
	*/

	for _, vol := range pod.Spec.Volumes {

		switch {
		case vol.VolumeSource.ConfigMap != nil:
			cmvs := vol.VolumeSource.ConfigMap

			podConfigMapDir := filepath.Join(api.PodVolRoot, BuildRemoteExecutionPodName(pod)+"/", mountSpec.Name)
			configMap, err := p.resourceManager.GetConfigMap(cmvs.Name, pod.GetNamespace())

			if cmvs.Optional != nil && !*cmvs.Optional {
				return errors.Wrapf(err, "Configmap '%s' is required by Pod '%s' and does not exist", cmvs.Name, pod.GetName())
			}

			if err != nil {
				return errors.Wrapf(err, "Error getting configmap '%s' from API server", pod.Name)
			}

			if configMap == nil {
				continue
			}

			if _, err := SSHClient.Exec("mkdir -p " + podConfigMapDir); err != nil {
				return errors.Wrapf(err, "cannot create '%s'", podConfigMapDir)
			}

			for k, v := range configMap.Data {
				// TODO: Ensure that these files are deleted in failure cases
				fullPath := filepath.Join(podConfigMapDir, k)
				if err := UploadData([]byte(v), fullPath, fs.FileMode(*cmvs.DefaultMode)); err != nil {
					return errors.Wrapf(err, "cannot write config map file '%s'", fullPath)
				}
			}

		case vol.VolumeSource.Secret != nil:
			svs := vol.VolumeSource.Secret
			podSecretDir := filepath.Join(api.PodVolRoot, BuildRemoteExecutionPodName(pod)+"/", vol.Name)

			secret, err := p.resourceManager.GetSecret(svs.SecretName, pod.Namespace)
			if svs.Optional != nil && !*svs.Optional {
				return errors.Errorf("Secret %s is required by Pod %s and does not exist", svs.SecretName, pod.Name)
			}
			if err != nil {
				return errors.Wrapf(err, "cannot get secret '%s' from API Server", pod.GetName())
			}

			if secret == nil {
				continue
			}

			if _, err := SSHClient.Exec("mkdir -p " + podSecretDir); err != nil {
				return errors.Wrapf(err, "cannot create dir '%s' for secrets", podSecretDir)
			}

			for k, v := range secret.Data {
				fullPath := filepath.Join(podSecretDir, k)

				if err := UploadData(v, fullPath, fs.FileMode(*svs.DefaultMode)); err != nil {
					return errors.Wrapf(err, "Could not write secret file %s", fullPath)
				}
			}

		default:
			// pod-global directory
			edPath := filepath.Join(api.PodVolRoot, BuildRemoteExecutionPodName(pod)+"/"+vol.Name)
			// mounted for every container
			if _, err := SSHClient.Exec("mkdir -p " + edPath); err != nil {
				return errors.Wrapf(err, "cannot create emptyDir '%s'", edPath)
			}
			// without size limit for now
		}
	}

	return nil
}

func checkPodsStatus(p *Provider, ctx context.Context) error {
	if len(p.pods) == 0 {
		return nil
	}

	log.GetLogger(ctx).Debug("received checkPodStatus")
	client, err := simplessh.ConnectWithKey(os.Getenv("REMOTE_HOST")+":"+os.Getenv("REMOTE_PORT"), os.Getenv("REMOTE_USER"), os.Getenv("REMOTE_KEY"))
	if err != nil {
		return errors.Wrapf(err, "cannot connnect")
	}

	defer client.Close()

	instanceName := ""
	now := metav1.Now()

	for _, pod := range p.pods {
		if pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed || pod.Status.Phase == corev1.PodPending {
			continue
		}

		// if it's not initialized yet
		if pod.Status.Conditions[0].Status == corev1.ConditionFalse && pod.Status.Conditions[0].Type == corev1.PodInitialized {
			containersCount := len(pod.Spec.InitContainers)
			successfull := 0
			failed := 0
			valid := 1

			for i, container := range pod.Spec.InitContainers {

				// TODO: find next initcontainer and run it
				instanceName = BuildRemoteExecutionInstanceName(pod, &pod.Spec.InitContainers[i])

				if len(pod.Status.InitContainerStatuses) < len(pod.Spec.InitContainers) {
					pod.Status.InitContainerStatuses = append(pod.Status.InitContainerStatuses, corev1.ContainerStatus{
						Name:         container.Name,
						Image:        container.Image,
						Ready:        true,
						RestartCount: 0,
						State: corev1.ContainerState{
							Running: &corev1.ContainerStateRunning{
								StartedAt: now,
							},
						},
					})
					continue
				}
				lastStatus := pod.Status.InitContainerStatuses[i]
				if lastStatus.Ready {

					statusFile, err := client.Exec("cat " + ".knoc/" + instanceName + ".status")
					status := string(statusFile)
					if len(status) > 1 {
						// remove '\n' from end of status due to golang's string conversion :X
						status = status[:len(status)-1]
					}

					if err != nil || status == "" {
						// still running
						continue
					}

					i, err := strconv.Atoi(status)
					reason := "Unknown"
					if i == 0 && err == nil {
						successfull++
						reason = "Completed"
					} else {
						failed++
						reason = "Error"
					}

					containersCount--
					pod.Status.InitContainerStatuses[i] = corev1.ContainerStatus{
						Name:  container.Name,
						Image: container.Image,
						Ready: false,
						State: corev1.ContainerState{
							Terminated: &corev1.ContainerStateTerminated{
								StartedAt:  lastStatus.State.Running.StartedAt,
								FinishedAt: now,
								Reason:     reason,
								ExitCode:   int32(i),
							},
						},
					}
					valid = 0
				} else {
					containersCount--
					status := lastStatus.State.Terminated.ExitCode
					i, _ := strconv.Atoi(string(status))
					if i == 0 {
						successfull++
					} else {
						failed++
					}
				}
			}
			if containersCount == 0 && pod.Status.Phase == corev1.PodRunning {
				if successfull == len(pod.Spec.InitContainers) {
					log.GetLogger(ctx).Debug("SUCCEEDED InitContainers")
					// PodInitialized = true
					pod.Status.Conditions[0].Status = corev1.ConditionTrue
					// PodReady = true
					pod.Status.Conditions[1].Status = corev1.ConditionTrue
					p.startMainContainers(ctx, pod)
					valid = 0
				} else {
					pod.Status.Phase = corev1.PodFailed
					valid = 0
				}
			}
			if valid == 0 {
				if err := p.UpdatePod(ctx, pod); err != nil {
					return errors.Wrapf(err, "update pod")
				}
			}
			// log.GetLogger(ctx).Infof("init checkPodStatus:%v %v %v", pod.Name, successfull, failed)
		} else {
			// if its initialized
			containersCount := len(pod.Spec.Containers)

			successfull := 0
			failed := 0
			valid := 1

			for i, container := range pod.Spec.Containers {

				instanceName = BuildRemoteExecutionInstanceName(pod, &pod.Spec.Containers[i])
				lastStatus := pod.Status.ContainerStatuses[i]
				if lastStatus.Ready {
					statusFile, err := client.Exec("cat " + ".knoc/" + instanceName + ".status")
					status := string(statusFile)
					if len(status) > 1 {
						// remove '\n' from end of status due to golang's string conversion :X
						status = status[:len(status)-1]
					}
					if err != nil || status == "" {
						// still running
						continue
					}
					containersCount--
					i, err := strconv.Atoi(status)
					reason := "Unknown"
					if i == 0 && err == nil {
						successfull++
						reason = "Completed"
					} else {
						failed++
						reason = "Error"
						// log.GetLogger(ctx).Info("[checkPodStatus] CONTAINER_FAILED")
					}

					pod.Status.ContainerStatuses[i] = corev1.ContainerStatus{
						Name:  container.Name,
						Image: container.Image,
						Ready: false,
						State: corev1.ContainerState{
							Terminated: &corev1.ContainerStateTerminated{
								StartedAt:  lastStatus.State.Running.StartedAt,
								FinishedAt: now,
								Reason:     reason,
								ExitCode:   int32(i),
							},
						},
					}
					valid = 0
				} else {
					if lastStatus.State.Terminated == nil {
						// containers not yet turned on
						if p.activeInitContainers(pod) {
							continue
						}
					}
					containersCount--
					status := lastStatus.State.Terminated.ExitCode

					i := status
					if i == 0 && err == nil {
						successfull++
					} else {
						failed++
					}
				}
			}
			if containersCount == 0 && pod.Status.Phase == corev1.PodRunning {
				// containers are ready
				pod.Status.Conditions[1].Status = corev1.ConditionFalse

				if successfull == len(pod.Spec.Containers) {
					log.GetLogger(ctx).Debug("[checkPodStatus] POD_SUCCEEDED ")
					pod.Status.Phase = corev1.PodSucceeded
				} else {
					log.GetLogger(ctx).Debug("[checkPodStatus] POD_FAILED ", successfull, " ", containersCount, " ", len(pod.Spec.Containers), " ", failed)
					pod.Status.Phase = corev1.PodFailed
				}
				valid = 0
			}

			if valid == 0 {
				if err := p.UpdatePod(ctx, pod); err != nil {
					return errors.Wrapf(err, "update pod")
				}
			}

			log.GetLogger(ctx).Debugf("main checkPodStatus:%v %v %v", pod.Name, successfull, failed)
		}
	}

	return nil
}
