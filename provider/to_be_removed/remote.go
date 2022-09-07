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

package to_be_removed

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"

	"github.com/carv-ics-forth/knoc/api"
	"github.com/carv-ics-forth/knoc/provider"
	"github.com/pkg/errors"
	"github.com/sfreiberg/simplessh"
	"github.com/sirupsen/logrus"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	corev1 "k8s.io/api/core/v1"
)

var SSHClient *simplessh.Client

func init() {
	/*
		client, err := simplessh.ConnectWithKey(os.Getenv("REMOTE_HOST")+":"+os.Getenv("REMOTE_PORT"), os.Getenv("REMOTE_USER"), os.Getenv("REMOTE_KEY"))
		if err != nil {
			panic(err)
		}

		SSHClient = client

		if err := prepareDoor(); err != nil {
			panic(err)
		}

	*/
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

func RemoteExecution(p *provider.Provider, ctx context.Context, mode api.Operation, pod *corev1.Pod) error {
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

func PrepareExecutionEnvironment(p *provider.Provider, ctx context.Context, pod *corev1.Pod) error {
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
			panic("not yet implemented")
			/*
				cmvs := vol.VolumeSource.ConfigMap

				configMap, err := p.resourceManager.GetConfigMap(cmvs.Name, pod.GetNamespace())
				{ // err check
					if cmvs.Optional != nil && !*cmvs.Optional {
						return errors.Wrapf(err, "Configmap '%s' is required by Pod '%s' and does not exist", cmvs.Name, pod.GetName())
					}

					if err != nil {
						return errors.Wrapf(err, "Error getting configmap '%s' from API server", pod.Name)
					}

					if configMap == nil {
						continue
					}
				}

				// .knoc/podName/volName/*
				podConfigMapDir := filepath.Join(api.RuntimeDir, pod.GetName(), cmvs.Name)

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

			*/

		case vol.VolumeSource.Secret != nil:
			panic("not yet implemented")

			/*
				svs := vol.VolumeSource.Secret

				secret, err := p.resourceManager.GetSecret(svs.SecretName, pod.Namespace)
				{
					if svs.Optional != nil && !*svs.Optional {
						return errors.Errorf("Secret %s is required by Pod %s and does not exist", svs.SecretName, pod.Name)
					}
					if err != nil {
						return errors.Wrapf(err, "cannot get secret '%s' from API Server", pod.GetName())
					}

					if secret == nil {
						continue
					}
				}

				// .knoc/podName/secretName/*
				podSecretDir := filepath.Join(api.RuntimeDir, pod.GetName(), svs.SecretName)

				if _, err := SSHClient.Exec("mkdir -p " + podSecretDir); err != nil {
					return errors.Wrapf(err, "cannot create dir '%s' for secrets", podSecretDir)
				}

				for k, v := range secret.Data {
					fullPath := filepath.Join(podSecretDir, k)

					if err := UploadData(v, fullPath, fs.FileMode(*svs.DefaultMode)); err != nil {
						return errors.Wrapf(err, "Could not write secret file %s", fullPath)
					}
				}

			*/

		default:
			// .knoc/podName/*
			edPath := filepath.Join(api.RuntimeDir, vol.Name)
			// mounted for every container
			if _, err := SSHClient.Exec("mkdir -p " + edPath); err != nil {
				return errors.Wrapf(err, "cannot create emptyDir '%s'", edPath)
			}
			// without size limit for now
		}
	}

	return nil
}
