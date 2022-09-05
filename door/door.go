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

package door

import (
	b64 "encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/carv-ics-forth/knoc/api"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

const (
	SBATCH  = "/usr/bin/sbatch"
	SCANCEL = "/usr/bin/scancel"
)

var buildVersion = "dev"

func prepareEnv(c *api.DoorContainer) []string {
	env := make([]string, 1)
	env = append(env, "--env")
	envData := ""

	for _, envVar := range c.Env {
		tmp := envVar.Name + "=" + envVar.Value + ","
		envData += tmp
	}

	if last := len(envData) - 1; last >= 0 && envData[last] == ',' {
		envData = envData[:last]
	}

	return append(env, envData)
}

func prepareMounts(c *api.DoorContainer) []string {
	mount := make([]string, 1)
	mount = append(mount, "--bind")
	mountData := ""
	podName := strings.Split(c.Name, "-")

	for _, mountVar := range c.Template.S.VolumeMounts {
		path := ".knoc/" + strings.Join(podName[:6], "-") + "/" + mountVar.Name + ":" + mountVar.MountPath + ","
		mountData += path
	}

	if last := len(mountData) - 1; last >= 0 && mountData[last] == ',' {
		mountData = mountData[:last]
	}

	return append(mount, mountData)
}

func produceSlurmScript(c *api.DoorContainer, command []string) (string, error) {
	newpath := filepath.Join(".", ".tmp")
	err := os.MkdirAll(newpath, os.ModePerm)

	f, err := os.Create(".tmp/" + c.Name + ".sh")
	if err != nil {
		return "", errors.Wrap(err, "Cant create slurm_script")
	}

	var sbatchFlagsFromArgo []string

	var sbatchFlagsAsString = ""
	if slurmFlags, ok := c.Metadata.Annotations["slurm-job.knoc.io/flags"]; ok {
		sbatchFlagsFromArgo = strings.Split(slurmFlags, " ")

		logrus.Debugln(sbatchFlagsFromArgo)
	}

	if mpiFlags, ok := c.Metadata.Annotations["slurm-job.knoc.io/mpi-flags"]; ok {
		if mpiFlags != "true" {
			mpi := append([]string{"mpiexec", "-np", "$SLURM_NTASKS"}, strings.Split(mpiFlags, " ")...)
			command = append(mpi, command...)
		}

		logrus.Debugln(mpiFlags)
	}

	for _, slurmFlag := range sbatchFlagsFromArgo {
		sbatchFlagsAsString += "\n#SBATCH " + slurmFlag
	}

	sbatchMacros := "#!/bin/bash" +
		"\n#SBATCH --job-name=" + c.Name +
		sbatchFlagsAsString +
		"\n. ~/.bash_profile" +
		"\npwd; hostname; date\n"

	// TODO: fix the malakia
	f.WriteString(sbatchMacros + "\n" + strings.Join(command[:], " ") + " >> " + ".knoc/" + c.Name + ".out 2>> " + ".knoc/" + c.Name + ".err \n echo $? > " + ".knoc/" + c.Name + ".status")

	if err := f.Close(); err != nil {
		return "", errors.Wrap(err, "Close")
	}

	return ".tmp/" + c.Name + ".sh", nil
}

func slurmBatchSubmit(path string) string {
	var output []byte
	var err error
	output, err = exec.Command(SBATCH, path).Output()
	if err != nil {
		log.Fatalln("Could not run sbatch. " + err.Error())
	}
	return string(output)

}

func handleJobID(c *api.DoorContainer, output string) error {
	r := regexp.MustCompile(`Submitted batch job (?P<jid>\d+)`)
	jid := r.FindStringSubmatch(output)
	f, err := os.Create(".knoc/" + c.Name + ".jid")
	if err != nil {
		return errors.Wrap(err, "Cant create jid_file")
	}
	f.WriteString(jid[1])

	return f.Close()
}

func CreateContainer(c *api.DoorContainer) error {
	logrus.Debugln("create_container")

	command := []string{"singularity", "exec"}

	envs := prepareEnv(c)
	mounts := prepareMounts(c)

	image := "docker://" + c.Image

	if strings.HasPrefix(c.Image, "/") {

		if imageURI, ok := c.GetObjectMeta().GetAnnotations()["slurm-job.knoc.io/image-root"]; ok {
			logrus.Debugln(imageURI)
			image = imageURI + c.Image
		} else {
			return errors.Errorf("image-uri annotation not specified for path in remote filesystem")
		}
	}

	singularityCommand := append(command, envs...)
	singularityCommand = append(singularityCommand, mounts...)
	singularityCommand = append(singularityCommand, image)
	singularityCommand = append(singularityCommand, c.Command...)
	singularityCommand = append(singularityCommand, c.Args...)

	path, err := produceSlurmScript(c, singularityCommand)
	if err != nil {
		return errors.Wrap(err, "cannot generate slurm script")
	}

	out := slurmBatchSubmit(path)

	if err := handleJobID(c, out); err != nil {
		return errors.Wrapf(err, "handleJobID")
	}

	logrus.Debugln(singularityCommand)
	logrus.Infoln(out)

	return nil
}

func DeleteContainer(c *api.DoorContainer) error {
	data, err := os.ReadFile(".knoc/" + c.Name + ".jobID")
	if err != nil {
		return errors.Wrapf(err, "Can't find job id of container '%s'", c.Name)
	}

	jobID, err := strconv.Atoi(string(data))
	if err != nil {
		return errors.Wrapf(err, "Can't find job id of container '%s'", c.Name)
	}

	_, err = exec.Command(SCANCEL, fmt.Sprint(jobID)).Output()
	if err != nil {
		return errors.Wrapf(err, "Could not delete job '%s'", c.Name)
	}

	exec.Command("rm", "-f ", ".knoc/"+c.Name+".out")
	exec.Command("rm", "-f ", ".knoc/"+c.Name+".err")
	exec.Command("rm", "-f ", ".knoc/"+c.Name+".status")
	exec.Command("rm", "-f ", ".knoc/"+c.Name+".jobID")
	exec.Command("rm", "-rf", " .knoc/"+c.Name)

	logrus.Info("Delete job", jobID)

	return nil
}

func ImportContainerb64Json(containerSpec string) (*api.DoorContainer, error) {
	dc := api.DoorContainer{}

	sDec, err := b64.StdEncoding.DecodeString(containerSpec)
	if err != nil {
		return nil, errors.Wrap(err, "Wrong containerSpec!")
	}

	if err = json.Unmarshal(sDec, &dc); err != nil {
		return nil, errors.Wrap(err, "Wrong type of doorContainer!")
	}

	return &dc, nil
}

/*
func normalizeImageName(instanceName string) string {
	instancesStr := strings.Split(instanceName, "/")
	finalName := ""
	firstIter := true

	for _, strings := range instancesStr {
		if firstIter {
			finalName = strings
			firstIter = false
			continue
		}
		finalName = finalName + "-" + strings
	}

	return strings.Split(finalName, ":")[0]
}

func BuildRemoteExecutionInstanceName(pod *corev1.Pod, container *corev1.Container) string {
	return pod.Namespace + "-" + string(pod.UID) + "-" + normalizeImageName(container.Image)
}
func BuildRemoteExecutionPodName(pod *corev1.Pod) string {
	return pod.Namespace + "-" + string(pod.UID)
}

*/

/*

	// in case we have initContainers we need to stop main containers from executing for now ...
	if len(pod.Spec.InitContainers) > 0 {
		state = waitingState
		hasInitContainers = true

		// run init container with remote execution enabled
		for i := range pod.Spec.InitContainers {
			// MUST TODO: Run init containers sequentialy and NOT all-together
			if err := RemoteExecution(p, ctx, api.SUBMIT, pod, &pod.Spec.InitContainers[i]); err != nil {
				return errors.Wrap(err, "remote execution failed")
			}
		}

		pod.Status = corev1.PodStatus{
			Phase:     corev1.PodRunning,
			HostIP:    "127.0.0.1",
			PodIP:     "127.0.0.1",
			StartTime: &now,
			Conditions: []corev1.PodCondition{
				{
					Type:   corev1.PodInitialized,
					Status: corev1.ConditionFalse,
				},
				{
					Type:   corev1.PodReady,
					Status: corev1.ConditionFalse,
				},
				{
					Type:   corev1.PodScheduled,
					Status: corev1.ConditionTrue,
				},
			},
		}
	} else {
		pod.Status = corev1.PodStatus{
			Phase:     corev1.PodRunning,
			HostIP:    "127.0.0.1",
			PodIP:     "127.0.0.1",
			StartTime: &now,
			Conditions: []corev1.PodCondition{
				{
					Type:   corev1.PodInitialized,
					Status: corev1.ConditionTrue,
				},
				{
					Type:   corev1.PodReady,
					Status: corev1.ConditionTrue,
				},
				{
					Type:   corev1.PodScheduled,
					Status: corev1.ConditionTrue,
				},
			},
		}
	}
	// deploy main containers
	for i, container := range pod.Spec.Containers {
		var err error

		if !hasInitContainers {
			if err := RemoteExecution(p, ctx, api.SUBMIT, pod, i); err != nil {
				return errors.Wrapf(err, "cannot execute container '%s'", container.Name)
			}
		}
		if err != nil {
			pod.Status.ContainerStatuses = append(pod.Status.ContainerStatuses, corev1.ContainerStatus{
				Name:         container.Name,
				Image:        container.Image,
				Ready:        false,
				RestartCount: 1,
				State: corev1.ContainerState{
					Terminated: &corev1.ContainerStateTerminated{
						Message:   "Could not reach remote cluster",
						StartedAt: now,
						ExitCode:  130,
					},
				},
			})
			pod.Status.Phase = corev1.PodFailed
			continue
		}
		pod.Status.ContainerStatuses = append(pod.Status.ContainerStatuses, corev1.ContainerStatus{
			Name:         container.Name,
			Image:        container.Image,
			Ready:        !hasInitContainers,
			RestartCount: 1,
			State:        state,
		})

	}
*/
