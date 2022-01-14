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

package knoc

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	b64 "encoding/base64"

	common "github.com/CARV-ICS-FORTH/knoc/common"

	"github.com/containerd/containerd/log"
	"github.com/pkg/sftp"
	"github.com/sfreiberg/simplessh"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func getRoutine(mode int8) string {
	switch mode {
	case 0:
		return "Create Remote Execution"
	case 1:
		return "Delete Remote Execution"
	default:
		return "UNKNOWN"

	}
}

func normalizeImageName(instance_name string) string {
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

func exportContainerb64Json(instance_name string, obj v1.Container, meta metav1.ObjectMeta) string {
	obj.Name = instance_name
	dc := common.DoorContainer{}
	dc.Args = obj.Args
	dc.Command = obj.Command
	dc.Env = obj.Env
	dc.EnvFrom = obj.EnvFrom
	dc.Image = obj.Image
	dc.Name = obj.Name
	dc.Ports = obj.Ports
	dc.Resources = obj.Resources
	dc.VolumeDevices = obj.VolumeDevices
	dc.VolumeMounts = obj.VolumeMounts
	dc.Metadata = meta

	u, _ := json.Marshal(dc)
	sEnc := b64.StdEncoding.EncodeToString(u)
	return sEnc
}

func hasDoor(client *simplessh.Client) bool {
	_, err := client.Exec("./door --version")
	return err == nil
}

func prepareDoor(client *simplessh.Client) {
	if !hasDoor(client) {
		// Could not find KNoC's Door binary in the remote system...
		// send door to remote
		local := "/usr/local/bin/door" // from inside the container's root dir
		remote := "door"
		common.UploadFile(client, local, remote, 0700)
		// check again else die
		_, err := client.Exec("./door --version")
		if err != nil {
			fmt.Println("Could not upload KNoC's Door")
			panic(err)
		}
	}
}

func runRemoteExecutionInstance(ctx context.Context, client *simplessh.Client, imageLocation string, instance_name string, container v1.Container, meta metav1.ObjectMeta) ([]byte, error) {
	b64dc := exportContainerb64Json(instance_name, container, meta)
	output, err := client.Exec("bash -l -c \"nohup ./door -a submit -c " + b64dc + " -V >> .knoc/door.log 2>> .knoc/door.log < /dev/null & \"")
	log.G(ctx).Debugf("bash -l -c \"nohup ./door -a submit -c " + b64dc + " -V >> .knoc/door.log 2>> .knoc/door.log < /dev/null & \"")
	if err != nil {
		// Could not exec instance
		return nil, err
	}
	return output, nil
}

func BuildRemoteExecutionInstanceName(container v1.Container, pod *v1.Pod) string {
	return pod.Namespace + "-" + string(pod.UID) + "-" + normalizeImageName(container.Image)
}
func BuildRemoteExecutionPodName(pod *v1.Pod) string {
	return pod.Namespace + "-" + string(pod.UID)
}

func stopRemoteExecutionInstance(ctx context.Context, client *simplessh.Client, pod *v1.Pod, instance_name string, container v1.Container, meta metav1.ObjectMeta) ([]byte, error) {
	b64dc := exportContainerb64Json(instance_name, container, meta)
	output, err := client.Exec("bash -l -c \"nohup ./door -a stop -c " + b64dc + " -V >> .knoc/door.log 2>> .knoc/door.log < /dev/null & \"")
	log.G(ctx).Debugf("bash -l -c \"nohup ./door -a stop -c " + b64dc + " -V >> .knoc/door.log 2>> .knoc/door.log < /dev/null & \"")
	if err != nil {
		// Could not exec instance
		return nil, err
	}

	return output, nil

}
func RemoteExecution(p *KNOCProvider, ctx context.Context, mode int8, imageLocation string, pod *v1.Pod, container v1.Container) error {
	var err error
	instance_name := BuildRemoteExecutionInstanceName(container, pod)
	client, err := simplessh.ConnectWithKey(os.Getenv("REMOTE_HOST")+":"+os.Getenv("REMOTE_PORT"), os.Getenv("REMOTE_USER"), os.Getenv("REMOTE_KEY"))
	if err != nil {
		return err
	}
	defer client.Close()
	log.GetLogger(ctx).Info(getRoutine(mode) + " Container")
	prepareDoor(client)
	if mode == common.CREATE {

		err = PrepareContainerData(p, ctx, client, instance_name, container, pod)
		if err != nil {
			return err
		}
		_, err = runRemoteExecutionInstance(ctx, client, imageLocation, instance_name, container, pod.ObjectMeta)
	} else if mode == common.DELETE {
		_, err = stopRemoteExecutionInstance(ctx, client, pod, instance_name, container, pod.ObjectMeta)
	}
	if err != nil {
		return err
	}

	return nil
}

func PrepareContainerData(p *KNOCProvider, ctx context.Context, client *simplessh.Client, instance_name string, container v1.Container, pod *v1.Pod) error {
	log.G(ctx).Debugf("receive prepareContainerData %v", container.Name)
	c, err := sftp.NewClient(client.SSHClient)
	if err != nil {
		fmt.Println("Could not connect over sftp on the remote system ")
		panic(err)
	}
	defer c.Close()

	//add kubeconfig on remote:$HOME
	out, err := exec.Command("test -f .kube/config").Output()
	if _, ok := err.(*exec.ExitError); !ok {
		log.GetLogger(ctx).Debug("Kubeconfig doesn't exist, so we will generate it...")
		out, err = exec.Command("/bin/sh", "/home/user0/scripts/prepare_kubeconfig.sh").Output()
		if err != nil {
			log.GetLogger(ctx).Errorln("Could not run kubeconfig_setup script!")
			log.GetLogger(ctx).Error(string(out))
			panic(err)
		}
		log.GetLogger(ctx).Debug("Kubeconfig generated")
		client.Exec("mkdir -p .kube")
		_, err = client.Exec("echo \"" + string(out) + "\" > .kube/config")
		if err != nil {
			log.GetLogger(ctx).Errorln("Could not setup kubeconfig on the remote system ")
			panic(err)
		}
		log.GetLogger(ctx).Debug("Kubeconfig installed")
	}

	client.Exec("mkdir -p " + ".knoc")
	for _, mountSpec := range container.VolumeMounts {
		podVolSpec := findPodVolumeSpec(pod, mountSpec.Name)
		if podVolSpec.ConfigMap != nil {
			cmvs := podVolSpec.ConfigMap
			mode := podVolSpec.ConfigMap.DefaultMode
			podConfigMapDir := filepath.Join(common.PodVolRoot, BuildRemoteExecutionPodName(pod)+"/", mountSpec.Name)
			configMap, err := p.resourceManager.GetConfigMap(cmvs.Name, pod.Namespace)
			if cmvs.Optional != nil && !*cmvs.Optional {
				return fmt.Errorf("Configmap %s is required by Pod %s and does not exist", cmvs.Name, pod.Name)
			}
			if err != nil {
				return fmt.Errorf("Error getting configmap %s from API server: %v", pod.Name, err)
			}
			if configMap == nil {
				continue
			}
			client.Exec("mkdir -p " + podConfigMapDir)
			log.GetLogger(ctx).Debugf("%v", "create dir for configmaps "+podConfigMapDir)

			for k, v := range configMap.Data {
				// TODO: Ensure that these files are deleted in failure cases
				fullPath := filepath.Join(podConfigMapDir, k)
				common.UploadData(client, []byte(v), fullPath, fs.FileMode(*mode))
				if err != nil {
					return fmt.Errorf("Could not write configmap file %s", fullPath)
				}
			}
		} else if podVolSpec.Secret != nil {
			svs := podVolSpec.Secret
			mode := podVolSpec.Secret.DefaultMode
			podSecretDir := filepath.Join(common.PodVolRoot, BuildRemoteExecutionPodName(pod)+"/", mountSpec.Name)
			secret, err := p.resourceManager.GetSecret(svs.SecretName, pod.Namespace)
			if svs.Optional != nil && !*svs.Optional {
				return fmt.Errorf("Secret %s is required by Pod %s and does not exist", svs.SecretName, pod.Name)
			}
			if err != nil {
				return fmt.Errorf("Error getting secret %s from API server: %v", pod.Name, err)
			}
			if secret == nil {
				continue
			}
			client.Exec("mkdir -p " + podSecretDir)
			log.GetLogger(ctx).Debugf("%v", "create dir for secrets "+podSecretDir)
			for k, v := range secret.Data {
				fullPath := filepath.Join(podSecretDir, k)
				common.UploadData(client, []byte(v), fullPath, fs.FileMode(*mode))
				if err != nil {
					return fmt.Errorf("Could not write secret file %s", fullPath)
				}
			}
		} else if podVolSpec.EmptyDir != nil {
			// pod-global directory
			edPath := filepath.Join(common.PodVolRoot, BuildRemoteExecutionPodName(pod)+"/"+mountSpec.Name)
			// mounted for every container
			client.Exec("mkdir -p " + edPath)
			// without size limit for now

		}
	}
	return nil
}

// Search for a particular volume spec by name in the Pod spec
func findPodVolumeSpec(pod *v1.Pod, name string) *v1.VolumeSource {
	for _, volume := range pod.Spec.Volumes {
		if volume.Name == name {
			return &volume.VolumeSource
		}
	}
	return nil
}

func checkPodsStatus(p *KNOCProvider, ctx context.Context) {
	if len(p.pods) == 0 {
		return
	}
	log.GetLogger(ctx).Debug("received checkPodStatus")
	client, err := simplessh.ConnectWithKey(os.Getenv("REMOTE_HOST")+":"+os.Getenv("REMOTE_PORT"), os.Getenv("REMOTE_USER"), os.Getenv("REMOTE_KEY"))
	if err != nil {
		panic(err)
	}
	defer client.Close()
	instance_name := ""
	now := metav1.Now()
	for _, pod := range p.pods {
		if pod.Status.Phase == v1.PodSucceeded || pod.Status.Phase == v1.PodFailed || pod.Status.Phase == v1.PodPending {
			continue
		}
		// if its not initialized yet
		if pod.Status.Conditions[0].Status == v1.ConditionFalse && pod.Status.Conditions[0].Type == v1.PodInitialized {
			containers_count := len(pod.Spec.InitContainers)
			successfull := 0
			failed := 0
			valid := 1
			for idx, container := range pod.Spec.InitContainers {
				//TODO: find next initcontainer and run it
				instance_name = BuildRemoteExecutionInstanceName(container, pod)
				if len(pod.Status.InitContainerStatuses) < len(pod.Spec.InitContainers) {
					pod.Status.InitContainerStatuses = append(pod.Status.InitContainerStatuses, v1.ContainerStatus{
						Name:         container.Name,
						Image:        container.Image,
						Ready:        true,
						RestartCount: 0,
						State: v1.ContainerState{
							Running: &v1.ContainerStateRunning{
								StartedAt: now,
							},
						},
					})
					continue
				}
				lastStatus := pod.Status.InitContainerStatuses[idx]
				if lastStatus.Ready {
					status_file, err := client.Exec("cat " + ".knoc/" + instance_name + ".status")
					status := string(status_file)
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
					containers_count--
					pod.Status.InitContainerStatuses[idx] = v1.ContainerStatus{
						Name:  container.Name,
						Image: container.Image,
						Ready: false,
						State: v1.ContainerState{
							Terminated: &v1.ContainerStateTerminated{
								StartedAt:  lastStatus.State.Running.StartedAt,
								FinishedAt: now,
								Reason:     reason,
								ExitCode:   int32(i),
							},
						},
					}
					valid = 0
				} else {
					containers_count--
					status := lastStatus.State.Terminated.ExitCode
					i, _ := strconv.Atoi(string(status))
					if i == 0 {
						successfull++
					} else {
						failed++
					}
				}
			}
			if containers_count == 0 && pod.Status.Phase == v1.PodRunning {
				if successfull == len(pod.Spec.InitContainers) {
					log.GetLogger(ctx).Debug("SUCCEEDED InitContainers")
					// PodInitialized = true
					pod.Status.Conditions[0].Status = v1.ConditionTrue
					// PodReady = true
					pod.Status.Conditions[1].Status = v1.ConditionTrue
					p.startMainContainers(ctx, pod)
					valid = 0
				} else {
					pod.Status.Phase = v1.PodFailed
					valid = 0
				}
			}
			if valid == 0 {
				p.UpdatePod(ctx, pod)
			}
			// log.GetLogger(ctx).Infof("init checkPodStatus:%v %v %v", pod.Name, successfull, failed)
		} else {
			// if its initialized
			containers_count := len(pod.Spec.Containers)
			successfull := 0
			failed := 0
			valid := 1
			for idx, container := range pod.Spec.Containers {
				instance_name = BuildRemoteExecutionInstanceName(container, pod)
				lastStatus := pod.Status.ContainerStatuses[idx]
				if lastStatus.Ready {
					status_file, err := client.Exec("cat " + ".knoc/" + instance_name + ".status")
					status := string(status_file)
					if len(status) > 1 {
						// remove '\n' from end of status due to golang's string conversion :X
						status = status[:len(status)-1]
					}
					if err != nil || status == "" {
						// still running
						continue
					}
					containers_count--
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
					pod.Status.ContainerStatuses[idx] = v1.ContainerStatus{
						Name:  container.Name,
						Image: container.Image,
						Ready: false,
						State: v1.ContainerState{
							Terminated: &v1.ContainerStateTerminated{
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
						if p.initContainersActive(pod) {
							continue
						}
					}
					containers_count--
					status := lastStatus.State.Terminated.ExitCode

					i := status
					if i == 0 && err == nil {
						successfull++
					} else {
						failed++
					}
				}
			}
			if containers_count == 0 && pod.Status.Phase == v1.PodRunning {
				// containers are ready
				pod.Status.Conditions[1].Status = v1.ConditionFalse

				if successfull == len(pod.Spec.Containers) {
					log.GetLogger(ctx).Debug("[checkPodStatus] POD_SUCCEEDED ")
					pod.Status.Phase = v1.PodSucceeded
				} else {
					log.GetLogger(ctx).Debug("[checkPodStatus] POD_FAILED ", successfull, " ", containers_count, " ", len(pod.Spec.Containers), " ", failed)
					pod.Status.Phase = v1.PodFailed
				}
				valid = 0
			}
			if valid == 0 {
				p.UpdatePod(ctx, pod)
			}
			log.GetLogger(ctx).Debugf("main checkPodStatus:%v %v %v", pod.Name, successfull, failed)

		}
	}

}
