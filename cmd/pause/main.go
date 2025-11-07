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

package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/carv-ics-forth/hpk/compute"
	"github.com/carv-ics-forth/hpk/compute/endpoint"
	"github.com/carv-ics-forth/hpk/compute/image"
	"github.com/carv-ics-forth/hpk/compute/podhandler"
	kubecontainer "github.com/carv-ics-forth/hpk/pkg/container"
	"github.com/rs/zerolog/log"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func getPodDetails(clientset *kubernetes.Clientset, namespace string, podID string) (*v1.Pod, error) {
	// Create a context with a 5-minute timeout
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	pod, err := clientset.CoreV1().Pods(namespace).Get(ctx, podID, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return pod, nil
}

func fileExists(filename string) bool {
	if filename == "" {
		return false
	}
	info, err := os.Stat(filename)
	if err != nil {
		if os.IsNotExist(err) {
			return false
		}
		panic(err)
	}
	return !info.IsDir() // Ensure it's a file, not a directory
}

func main() {
	var podID string
	var namespaceID string
	var wg sync.WaitGroup

	flag.StringVar(&podID, "pod", "", "Pod ID to query Kubernetes")
	flag.StringVar(&namespaceID, "namespace", "", "Pod ID to query Kubernetes")
	flag.Parse()

	if podID == "" || namespaceID == "" {
		log.Fatal().Msg("Please provide both the pod and namespace.")
	}

	config, err := clientcmd.BuildConfigFromFlags("", filepath.Join("/k8s-data", "admin.conf"))
	if err != nil {
		log.Fatal().Err(err).Msg("Error building kubeconfig")
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		log.Fatal().Err(err).Msg("Error creating Kubernetes client")
	}

	// Main loop to keep asking for pod details
	timeout := time.After(5 * time.Minute)

acquire_pod_loop:
	for {
		select {
		case <-timeout:
			log.Error().Msg("Timeout reached. Exiting.")
			os.Exit(1)
		default:
			_, err := getPodDetails(clientset, namespaceID, podID)
			if err != nil {
				log.Error().Err(err).Msg("Error getting pod details. Retrying...")
				time.Sleep(5 * time.Second) // Adjust retry interval as needed
				continue
			}

			break acquire_pod_loop
		}
	}

	pod, err := getPodDetails(clientset, namespaceID, podID)
	if err != nil {
		panic(err)
	}

	if err := prepareContainers(pod); err != nil {
		log.Error().Err(err).Msg("Error preparing container environment")
		return
	}

	if len(pod.Spec.InitContainers) > 0 {
		if err := handleInitContainers(pod, true); err != nil {
			log.Error().Err(err).Msg("Error executing init containers")
			return
		}
	}

	if err := handleContainers(pod, &wg, true); err != nil {
		log.Error().Err(err).Msg("Error executing main containers")
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM, syscall.SIGCHLD)

	go func() {
		for {
			select {
			case signo := <-signalChan:
				switch signo {
				case syscall.SIGINT, syscall.SIGTERM:
					// Termination handling of the pause container by external signals
					log.Info().Msgf("Received %v. Cleaning up...\n", signo)

					// Ensure completion of bookkeeping before handling sigchld
					wg.Wait()

					// Initiate cleanup
					cancel()

				case syscall.SIGCHLD:
					// SIGCHLD handling - reap zombie processes
					log.Info().Msg("Received SIGCHLD. Containers have terminated. ")

					// Ensure completion of bookkeeping before handling sigchld
					wg.Wait()
					for {
						pid, err := syscall.Wait4(-1, nil, syscall.WNOHANG, nil)
						if pid <= 0 {
							if err != nil {
								log.Error().Err(err).Msg("Error stopping hpk-pause")
							}
							break
						}
						log.Info().Msgf("pid: %v", pid)
					}

					// Initiate cleanup after SIGCHLD handling
					cancel()
				}
			case <-ctx.Done():
				log.Info().Msg("Containers and context have terminated. Exiting...")
				return
			}

		}
	}()

	log.Info().Msg("Containers have started. Now waiting on context or signals")
	<-ctx.Done()

}

func prepareContainers(pod *v1.Pod) error {
	if err := prepareDNS(pod); err != nil {
		return fmt.Errorf("could not prepare DNS : %v", err)
	}
	if err := announceIP(pod); err != nil {
		return fmt.Errorf("could not announce ip : %v", err)
	}
	if err := cleanEnvironment(); err != nil {
		return fmt.Errorf("could not clear the environment : %v", err)
	}
	return nil
}

func announceIP(pod *v1.Pod) error {
	podKey := client.ObjectKeyFromObject(pod)
	hpk := endpoint.HPK(pod.Annotations["workingDirectory"])
	podPath := hpk.Pod(podKey)

	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return fmt.Errorf("could not get interfaces from host: %v", err)
	}
	var ipAddresses []string
	for _, addr := range addrs {
		// Add only if the address is an IP address
		if ipNet, ok := addr.(*net.IPNet); ok && !ipNet.IP.IsLoopback() && ipNet.IP.To4() != nil {
			ipAddresses = append(ipAddresses, ipNet.IP.String())
		}
	}
	ipString := strings.Join(ipAddresses, " ")

	if err := os.WriteFile(podPath.IPAddressPath(), []byte(ipString), os.ModePerm); err != nil {
		return fmt.Errorf("error writing to .ip file: %v", err)
	}
	return nil
}

func cleanEnvironment() error {

	envVars := []string{
		"LD_LIBRARY_PATH",
		"SINGULARITY_COMMAND",
		"SINGULARITY_CONTAINER",
		"SINGULARITY_ENVIRONMENT",
		"SINGULARITY_NAME",
		"APPTAINER_APPNAME",
		"APPTAINER_COMMAND",
		"APPTAINER_CONTAINER",
		"APPTAINER_ENVIRONMENT",
		"APPTAINER_NAME",
		"APPTAINER_BIND",
		"SINGULARITY_BIND",
	}

	for _, name := range envVars {
		if err := os.Unsetenv(name); err != nil {
			return fmt.Errorf("could not clear the environment variable %s: %v", name, err)
		}
	}
	return nil
}

func prepareDNS(pod *v1.Pod) error {
	if err := os.MkdirAll("/scratch/etc", 0644); err != nil {
		return fmt.Errorf("could not create /scratch/etc folder: %v", err)
	}

	kubeDNSIP := os.Getenv("KUBEDNS_IP")
	if kubeDNSIP == "" {
		return fmt.Errorf("KUBEDNS_IP environment variable not set")
	}

	// Create and write to /scratch/etc/resolv.conf
	resolvConfContent := fmt.Sprintf(
		`
search %s.svc.cluster.local svc.cluster.local cluster.local
nameserver %s
options ndots:5
`, pod.Namespace, kubeDNSIP)

	if err := os.WriteFile("/scratch/etc/resolv.conf", []byte(resolvConfContent), os.ModePerm); err != nil {
		return fmt.Errorf("error writing to resolv.conf: %v", err)
	}

	// Add hostname to /scratch/etc/hosts
	hostname, err := os.Hostname()
	if err != nil {
		return fmt.Errorf("error getting hostname: %v", err)
	}

	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return fmt.Errorf("could not get interfaces from host: %v", err)
	}

	var ipAddresses []string
	for _, addr := range addrs {
		// Add only if the address is an IP address
		if ipNet, ok := addr.(*net.IPNet); ok && !ipNet.IP.IsLoopback() && ipNet.IP.To4() != nil {
			ipAddresses = append(ipAddresses, ipNet.IP.String())
		}
	}
	ipString := strings.Join(ipAddresses, " ") + " " + hostname

	hostsContent := fmt.Sprintf("127.0.0.1 localhost\n%s \n", ipString)

	if err := os.WriteFile("/scratch/etc/hosts", []byte(hostsContent), os.ModePerm); err != nil {
		return fmt.Errorf("error writing to hosts: %v", err)
	}
	DebugDNSInfo(resolvConfContent, hostsContent)
	return nil
}

func DebugDNSInfo(resolvConfContent string, hostsContent string) {
	fmt.Printf("====================================================================\n%s\n", resolvConfContent)
	fmt.Printf("====================================================================\n%s", hostsContent)
	fmt.Println("====================================================================")

}

func handleInitContainers(pod *v1.Pod, hpkEnv bool) error {
	isDebug := os.Getenv("DEBUG_MODE") == "true"
	podKey := client.ObjectKeyFromObject(pod)
	hpk := endpoint.HPK(pod.Annotations["workingDirectory"])
	podPath := hpk.Pod(podKey)
	for _, container := range pod.Spec.InitContainers {
		effectiSecurityContext := podhandler.DetermineEffectiveSecurityContext(pod, &container)
		uid, gid := podhandler.DetermineEffectiveRunAsUser(effectiSecurityContext)
		log.Info().Msgf("Spawning init container: %s", container.Name)
		instanceName := fmt.Sprintf("%s_%s_%s", pod.GetNamespace(), pod.GetName(), container.Name)

		containerPath := podPath.Container(container.Name)
		envFilePath := containerPath.EnvFilePath()

		// Environment File Handling
		if fileExists(envFilePath) {
			output, err := exec.Command("sh", "-c", envFilePath).CombinedOutput()
			if err != nil {
				return fmt.Errorf("error executing EnvFilePath: %v, output: %s", err, output)
			}
			envFileName := filepath.Join("/scratch", instanceName+".env")
			if err := os.WriteFile(envFileName, output, 0644); err != nil {
				return fmt.Errorf("error writing env file: %v", err)
			}
		}

		executionMode := "exec"
		if container.Command == nil {
			executionMode = "run"
		}

		binds := make([]string, len(container.VolumeMounts))

		// check the code from https://github.com/kubernetes/kubernetes/blob/master/pkg/kubelet/kubelet_pods.go#L196
		for i, mount := range container.VolumeMounts {
			hostPath := filepath.Join(podPath.VolumeDir(), mount.Name)

			subPath := mount.SubPath
			if mount.SubPathExpr != "" {

				path, err := kubecontainer.ExpandContainerVolumeMounts(mount, podhandler.FromServices(context.Background(), pod.Namespace))
				if err != nil {
					compute.SystemPanic(err, "cannot expand env variables for container '%s' of pod '%s'", container.Name, podKey)
				}
				subPath = path
			}

			if subPath != "" {
				if filepath.IsAbs(subPath) {
					return fmt.Errorf("error SubPath '%s' must not be an absolute path", subPath)
				}

				subPathFile := filepath.Join(hostPath, subPath)

				// mount the subpath
				hostPath = subPathFile
			}

			accessMode := "rw"
			if mount.ReadOnly {
				accessMode = "ro"
			}

			binds[i] = hostPath + ":" + mount.MountPath + ":" + accessMode
		}

		// Apptainer Command Construction
		apptainerVerbosity := "--quiet"
		if isDebug {
			apptainerVerbosity = "--debug"
		}
		apptainerArgs := []string{
			apptainerVerbosity, executionMode, "--nv", "--cleanenv", "--writable-tmpfs", "--no-mount", "home", "--unsquash",
		}
		if hpkEnv {
			apptainerArgs = append(apptainerArgs, "--bind", "/scratch/etc/resolv.conf:/etc/resolv.conf,/scratch/etc/hosts:/etc/hosts")
		}
		if len(binds) > 0 {
			bindArgs := &apptainerArgs[len(apptainerArgs)-1]
			*bindArgs += "," + strings.Join(binds, ",")
		}
		if uid != 0 {
			apptainerArgs = append(apptainerArgs, "--security", fmt.Sprintf("uid:%d,gid:%d", uid, uid), "--userns")
		}
		if gid != 0 {
			apptainerArgs = append(apptainerArgs, "--security", fmt.Sprintf("gid:%d", gid), "--userns")
		}

		if fileExists(envFilePath) {
			apptainerArgs = append(apptainerArgs, "--env-file", filepath.Join("/scratch", instanceName+".env"))
		}

		apptainerArgs = append(apptainerArgs, hpk.ImageDir()+image.ParseImageName(container.Image))
		apptainerArgs = append(apptainerArgs, kubecontainer.ExpandContainerCommandOnlyStatic(container.Command, container.Env)...)
		apptainerArgs = append(apptainerArgs, kubecontainer.ExpandContainerCommandOnlyStatic(container.Args, container.Env)...)

		// Get the PID
		pid := os.Getpid()
		if err := os.WriteFile(containerPath.IDPath(), []byte(fmt.Sprintf("pid://%d", pid)), 0644); err != nil {
			return fmt.Errorf("failed to create pid file") // Log the error
		}

		// Execute Apptainer (Blocking)
		log.Debug().Msg(fmt.Sprintf("ApptainerArgs: %v", apptainerArgs))
		cmd := exec.Command("apptainer", apptainerArgs...)
		cmd.Env = os.Environ()

		// Open log file
		logFile, err := os.Create(containerPath.LogsPath())
		if err != nil {
			return fmt.Errorf("failed to create log file: %v", err)
		}
		defer logFile.Close()

		// // Redirect output to log file
		cmd.Stdout = logFile
		cmd.Stderr = logFile
		if err := cmd.Run(); err != nil {
			log.Error().Err(err).Msgf("Error executing init container: %s", container.Name)
			return fmt.Errorf("init container failed: %v", err) // Abort on failure
		}
		if err := os.WriteFile(containerPath.ExitCodePath(), []byte(strconv.Itoa(cmd.ProcessState.ExitCode())), 0644); err != nil {
			return fmt.Errorf("failed to create exitCode file") // Log the error
		}
	}
	return nil
}

func handleContainers(pod *v1.Pod, wg *sync.WaitGroup, hpkEnv bool) error {
	isDebug := os.Getenv("DEBUG_MODE") == "true"
	podKey := client.ObjectKeyFromObject(pod)
	hpk := endpoint.HPK(pod.Annotations["workingDirectory"])
	podPath := hpk.Pod(podKey)
	for _, container := range pod.Spec.Containers {
		effectiSecurityContext := podhandler.DetermineEffectiveSecurityContext(pod, &container)
		uid, gid := podhandler.DetermineEffectiveRunAsUser(effectiSecurityContext)
		instanceName := fmt.Sprintf("%s_%s_%s", pod.GetNamespace(), pod.GetName(), container.Name)

		containerPath := podPath.Container(container.Name)
		envFilePath := containerPath.EnvFilePath()

		// Environment File Handling
		if fileExists(envFilePath) {
			output, err := exec.Command("sh", "-c", envFilePath).CombinedOutput()
			if err != nil {
				return fmt.Errorf("error executing EnvFilePath: %v, output: %s", err, output)
			}
			envFileName := filepath.Join("/scratch", instanceName+".env")
			if err := os.WriteFile(envFileName, output, 0644); err != nil {
				return fmt.Errorf("error writing env file: %v", err)
			}
		}

		executionMode := "exec"
		if container.Command == nil {
			executionMode = "run"
		}

		binds := make([]string, len(container.VolumeMounts))

		// check the code from https://github.com/kubernetes/kubernetes/blob/master/pkg/kubelet/kubelet_pods.go#L196
		for i, mount := range container.VolumeMounts {
			hostPath := filepath.Join(podPath.VolumeDir(), mount.Name)

			subPath := mount.SubPath
			if mount.SubPathExpr != "" {

				path, err := kubecontainer.ExpandContainerVolumeMounts(mount, podhandler.FromServices(context.Background(), pod.Namespace))
				if err != nil {
					compute.SystemPanic(err, "cannot expand env variables for container '%s' of pod '%s'", container.Name, podKey)
				}
				subPath = path
			}

			if subPath != "" {
				if filepath.IsAbs(subPath) {
					return fmt.Errorf("error SubPath '%s' must not be an absolute path", subPath)
				}

				subPathFile := filepath.Join(hostPath, subPath)

				// mount the subpath
				hostPath = subPathFile
			}

			accessMode := "rw"
			if mount.ReadOnly {
				accessMode = "ro"
			}

			binds[i] = hostPath + ":" + mount.MountPath + ":" + accessMode
		}

		// Apptainer Command Construction
		apptainerVerbosity := "--quiet"
		if isDebug {
			apptainerVerbosity = "--debug"
		}
		apptainerArgs := []string{
			apptainerVerbosity, executionMode, "--nv", "--cleanenv", "--writable-tmpfs", "--no-mount", "home", "--unsquash",
		}
		if hpkEnv {
			apptainerArgs = append(apptainerArgs, "--bind", "/scratch/etc/resolv.conf:/etc/resolv.conf,/scratch/etc/hosts:/etc/hosts")
			if len(binds) > 0 {
				bindArgs := &apptainerArgs[len(apptainerArgs)-1]
				*bindArgs += "," + strings.Join(binds, ",")
			}
		}
		if uid != 0 {
			apptainerArgs = append(apptainerArgs, "--security", fmt.Sprintf("uid:%d,gid:%d", uid, uid), "--userns")
		}
		if gid != 0 {
			apptainerArgs = append(apptainerArgs, "--security", fmt.Sprintf("gid:%d", gid), "--userns")
		}

		if fileExists(envFilePath) {
			apptainerArgs = append(apptainerArgs, "--env-file", filepath.Join("/scratch", instanceName+".env"))
		}

		apptainerArgs = append(apptainerArgs, hpk.ImageDir()+image.ParseImageName(container.Image))
		apptainerArgs = append(apptainerArgs, kubecontainer.ExpandContainerCommandOnlyStatic(container.Command, container.Env)...)
		apptainerArgs = append(apptainerArgs, kubecontainer.ExpandContainerCommandOnlyStatic(container.Args, container.Env)...)

		wg.Add(1)
		go func(container v1.Container) { // Ensure container cleanup
			defer wg.Done()
			// Execute Apptainer in Background
			log.Debug().Msg(fmt.Sprintf("ApptainerArgs: %v", apptainerArgs))
			cmd := exec.Command("apptainer", apptainerArgs...)
			cmd.Env = os.Environ()
			// If needed, get references to stdout and stderr
			// log.Debug().Msgf("LogPath: %s", containerPath.LogsPath())
			logFile, err := os.Create(containerPath.LogsPath())
			if err != nil {
				log.Error().Err(err).Msgf("Failed to create log file %s", containerPath.LogsPath())
				return
			}
			defer logFile.Close()

			cmd.Stdout = logFile
			cmd.Stderr = logFile
			log.Info().Msgf("Spawning main container: %s", container.Name)
			// Start the  container
			if err := cmd.Start(); err != nil {
				log.Error().Err(err).Msg("Failed to start Apptainer container")
			}

			// Get the PID
			pid := cmd.Process.Pid
			if err := os.WriteFile(containerPath.IDPath(), []byte(fmt.Sprintf("pid://%d", pid)), 0644); err != nil {
				log.Error().Err(err).Msg("Failed to create pid file") // Log the error
				return
			}

			// Handle Exit (consider moving output writing or using cmd.Wait)
			if err := cmd.Wait(); err != nil {
				log.Error().Err(err).Msgf("error executing container: %s, because of %v", container.Name, err)
			}

			if err := os.WriteFile(containerPath.ExitCodePath(), []byte(strconv.Itoa(cmd.ProcessState.ExitCode())), 0644); err != nil {
				log.Error().Err(err).Msg("Failed to create exitCode file") // Log the error
				return
			}

		}(container)

	}
	return nil
}
