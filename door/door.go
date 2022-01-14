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

package main

import (
	b64 "encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/akamensky/argparse"
	log "github.com/sirupsen/logrus"
)

const (
	SUBMIT  = 0
	STOP    = 1
	UNKNOWN = 2
	SBATCH  = "/usr/bin/sbatch"
	SCANCEL = "/usr/bin/scancel"
)

var buildVersion = "dev"

func args_parser() (int, string) {
	// Create new parser object
	parser := argparse.NewParser("door", "KNoC's sidekick, he's deadly!")
	// Create string flag
	// log.Infoln(buildVersion)
	var action *string = parser.Selector("a", "action", []string{"submit", "stop"}, &argparse.Options{Required: false, Help: "Action required for door"})
	var container *string = parser.String("c", "container", &argparse.Options{Required: false, Help: "Container required for action"})
	verbose := parser.Flag("V", "verbose", &argparse.Options{Required: false, Help: "Verbose flag sets log level to DEBUG"})
	version := parser.Flag("v", "version", &argparse.Options{Required: false, Help: "version"})

	// Parse input
	err := parser.Parse(os.Args)
	if err != nil {
		// In case of error print error and print usage
		// This can also be done by passing -h or --help flags
		fmt.Print(parser.Usage(err))
	}

	if *version {
		log.Infoln(buildVersion)
		os.Exit(0)
	}

	if *verbose {
		log.SetLevel(log.DebugLevel)
	}
	// log.Debugln(*action, *container)
	if *action == "submit" {
		return SUBMIT, *container
	} else if *action == "stop" {
		return STOP, *container
	}
	fmt.Println(parser.Usage(""))
	return 3, ""
}

func prepare_env(c DoorContainer) []string {
	env := make([]string, 1)
	env = append(env, "--env")
	env_data := ""
	for _, env_var := range c.Env {
		tmp := (env_var.Name + "=" + env_var.Value + ",")
		env_data += tmp
	}
	if last := len(env_data) - 1; last >= 0 && env_data[last] == ',' {
		env_data = env_data[:last]
	}
	return append(env, env_data)
}

func prepare_mounts(c DoorContainer) []string {
	mount := make([]string, 1)
	mount = append(mount, "--bind")
	mount_data := ""
	pod_name := strings.Split(c.Name, "-")
	for _, mount_var := range c.VolumeMounts {
		path := (".knoc/" + strings.Join(pod_name[:6], "-") + "/" + mount_var.Name + ":" + mount_var.MountPath + ",")
		mount_data += path
	}
	if last := len(mount_data) - 1; last >= 0 && mount_data[last] == ',' {
		mount_data = mount_data[:last]
	}
	return append(mount, mount_data)
}

func produce_slurm_script(c DoorContainer, command []string) string {
	newpath := filepath.Join(".", ".tmp")
	err := os.MkdirAll(newpath, os.ModePerm)
	f, err := os.Create(".tmp/" + c.Name + ".sh")
	if err != nil {
		log.Fatalln("Cant create slurm_script")
	}
	var sbatch_flags_from_argo []string
	var sbatch_flags_as_string = ""
	if slurm_flags, ok := c.Metadata.Annotations["slurm-job.knoc.io/flags"]; ok {
		sbatch_flags_from_argo = strings.Split(slurm_flags, " ")
		log.Debugln(sbatch_flags_from_argo)
	}
	if mpi_flags, ok := c.Metadata.Annotations["slurm-job.knoc.io/mpi-flags"]; ok {
		if mpi_flags != "true" {
			mpi := append([]string{"mpiexec", "-np", "$SLURM_NTASKS"}, strings.Split(mpi_flags, " ")...)
			command = append(mpi, command...)
		}
		log.Debugln(mpi_flags)
	}
	for _, slurm_flag := range sbatch_flags_from_argo {
		sbatch_flags_as_string += "\n#SBATCH " + slurm_flag
	}
	sbatch_macros := "#!/bin/bash" +
		"\n#SBATCH --job-name=" + c.Name +
		sbatch_flags_as_string +
		"\n. ~/.bash_profile" +
		"\npwd; hostname; date\n"
	f.WriteString(sbatch_macros + "\n" + strings.Join(command[:], " ") + " >> " + ".knoc/" + c.Name + ".out 2>> " + ".knoc/" + c.Name + ".err \n echo $? > " + ".knoc/" + c.Name + ".status")
	f.Close()
	return ".tmp/" + c.Name + ".sh"
}

func slurm_batch_submit(path string, c DoorContainer) string {
	var output []byte
	var err error
	output, err = exec.Command(SBATCH, path).Output()
	if err != nil {
		log.Fatalln("Could not run sbatch. " + err.Error())
	}
	return string(output)

}

func handle_jid(c DoorContainer, output string) {
	r := regexp.MustCompile(`Submitted batch job (?P<jid>\d+)`)
	jid := r.FindStringSubmatch(output)
	f, err := os.Create(".knoc/" + c.Name + ".jid")
	if err != nil {
		log.Fatalln("Cant create jid_file")
	}
	f.WriteString(jid[1])
	f.Close()
}

func create_container(c DoorContainer) {
	log.Debugln("create_container")

	commstr1 := []string{"singularity", "exec"}

	envs := prepare_env(c)
	image := ""
	mounts := prepare_mounts(c)
	if strings.HasPrefix(c.Image, "/") {
		if image_uri, ok := c.Metadata.Annotations["slurm-job.knoc.io/image-root"]; ok {
			log.Debugln(image_uri)
			image = image_uri + c.Image
		} else {
			log.Errorln("image-uri annotation not specified for path in remote filesystem")
		}
	} else {
		image = "docker://" + c.Image
	}
	singularity_command := append(commstr1, envs...)
	singularity_command = append(singularity_command, mounts...)
	singularity_command = append(singularity_command, image)
	singularity_command = append(singularity_command, c.Command...)
	singularity_command = append(singularity_command, c.Args...)

	path := produce_slurm_script(c, singularity_command)
	out := slurm_batch_submit(path, c)
	handle_jid(c, out)
	log.Debugln(singularity_command)
	log.Infoln(out)

}

func delete_container(c DoorContainer) {
	data, err := os.ReadFile(".knoc/" + c.Name + ".jid")
	if err != nil {
		log.Fatalln("Can't find job id of container")
	}
	jid, err := strconv.Atoi(string(data))
	if err != nil {
		log.Fatalln("Can't find job id of container")
	}
	_, err = exec.Command(SCANCEL, fmt.Sprint(jid)).Output()
	if err != nil {
		log.Fatalln("Could not delete job", jid)
	}
	exec.Command("rm", "-f ", ".knoc/"+c.Name+".out")
	exec.Command("rm", "-f ", ".knoc/"+c.Name+".err")
	exec.Command("rm", "-f ", ".knoc/"+c.Name+".status")
	exec.Command("rm", "-f ", ".knoc/"+c.Name+".jid")
	exec.Command("rm", "-rf", " .knoc/"+c.Name)
	log.Infoln("Delete job", jid)
}

func importContainerb64Json(containerSpec string) DoorContainer {
	dc := DoorContainer{}
	sDec, err := b64.StdEncoding.DecodeString(containerSpec)
	if err != nil {
		log.Fatalln("Wrong containerSpec!")
	}
	err = json.Unmarshal(sDec, &dc)
	if err != nil {
		log.Fatalln("Wrong type of doorContainer!")
	}
	return dc
}

func main() {
	action, containerSpec := args_parser()
	if action == 3 {
		os.Exit(1)
	}
	dc := importContainerb64Json(containerSpec)
	switch action {
	case SUBMIT:
		create_container(dc)
	case STOP:
		delete_container(dc)
	}
}
