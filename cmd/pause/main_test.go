package main

import (
	"fmt"
	"os"
	"os/exec"
	"sync"
	"testing"

	"github.com/carv-ics-forth/hpk/compute/endpoint"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Mock execFunc for testing
func mockExecFunc(command string, args ...string) *exec.Cmd {
	cs := []string{"-test.run=TestHelperProcess", "--", command}
	cs = append(cs, args...)
	cmd := exec.Command(os.Args[0], cs...)
	cmd.Env = []string{"GO_WANT_HELPER_PROCESS=1"}
	return cmd
}

// Test for successful init container execution
func TestHandleInitContainers_Success(t *testing.T) {

	// Create a test Pod
	annotations := make(map[string]string)
	annotations["workingDirectory"] = "/home/malvag"
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "test-init-pod",
			Namespace:   "default",
			Annotations: annotations,
		},
		Spec: v1.PodSpec{
			InitContainers: []v1.Container{
				{Name: "test-init-container", Image: "busybox", Command: []string{"sh", "-c", "echo hello from init container"}},
			},
		},
	}
	podKey := client.ObjectKeyFromObject(pod)
	hpk := endpoint.HPK(pod.Annotations["workingDirectory"])
	podPath := hpk.Pod(podKey)
	logPath := podPath.Container("test-init-container").LogsPath()
	exitCodeFilePath := podPath.Container("test-init-container").ExitCodePath()

	if err := os.MkdirAll(string(podPath), 0750); err != nil {
		t.Errorf("create pod directory failed unexpectedly: %v", err)
	}
	if err := os.MkdirAll(string(podPath.ControlFileDir()), 0750); err != nil {
		t.Errorf("create pod directory failed unexpectedly: %v", err)
	}
	if err := os.MkdirAll(string(podPath.JobDir()), 0750); err != nil {
		t.Errorf("create pod directory failed unexpectedly: %v", err)
	}
	if err := os.MkdirAll(string(podPath.LogDir()), 0750); err != nil {
		t.Errorf("create pod directory failed unexpectedly: %v", err)
	}

	if err := handleInitContainers(pod, false); err != nil {
		t.Errorf("handleInitContainers failed unexpectedly: %v", err)
	}
	//  Verify log file contents (adjust the path as needed based on your implementation)
	logData, err := os.ReadFile(logPath)
	if err != nil {
		t.Errorf("Error reading log file: %v", err)
	}

	expectedOutput := "hello from init container\n"
	if string(logData) != expectedOutput {
		t.Errorf("Unexpected log output. Got: %v, Expected: %v", string(logData), expectedOutput)
	}
	exitData, err := os.ReadFile(exitCodeFilePath)
	if err != nil {
		t.Errorf("Error reading exitCode file: %v", err)
	}

	if string(exitData) != fmt.Sprint(0) {
		t.Errorf("Unexpected exitCode. Got: %v, Expected: %v", string(exitData), 0)
	}
}

// Test for successful init container execution
func TestHandleContainers_Success(t *testing.T) {

	// Create a test Pod
	annotations := make(map[string]string)
	annotations["workingDirectory"] = "/home/malvag"
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "test-main-pod",
			Namespace:   "default",
			Annotations: annotations,
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{Name: "test-main-container", Image: "busybox", Command: []string{"sh", "-c", "echo hello from main container"}},
			},
		},
	}
	podKey := client.ObjectKeyFromObject(pod)
	hpk := endpoint.HPK(pod.Annotations["workingDirectory"])
	podPath := hpk.Pod(podKey)
	logPath := podPath.Container("test-main-container").LogsPath()
	exitCodeFilePath := podPath.Container("test-main-container").ExitCodePath()

	if err := os.MkdirAll(string(podPath), 0750); err != nil {
		t.Errorf("create pod directory failed unexpectedly: %v", err)
	}
	if err := os.MkdirAll(string(podPath.ControlFileDir()), 0750); err != nil {
		t.Errorf("create pod directory failed unexpectedly: %v", err)
	}
	if err := os.MkdirAll(string(podPath.JobDir()), 0750); err != nil {
		t.Errorf("create pod directory failed unexpectedly: %v", err)
	}
	if err := os.MkdirAll(string(podPath.LogDir()), 0750); err != nil {
		t.Errorf("create pod directory failed unexpectedly: %v", err)
	}

	var wg sync.WaitGroup
	if err := handleContainers(pod, &wg, false); err != nil {
		t.Errorf("handleContainers failed unexpectedly: %v", err)
	}

	wg.Wait()
	//  Verify log file contents (adjust the path as needed based on your implementation)
	logData, err := os.ReadFile(logPath)
	if err != nil {
		t.Errorf("Error reading log file: %v", err)
	}

	expectedOutput := "hello from main container\n"
	if string(logData) != expectedOutput {
		t.Errorf("Unexpected log output. Got: %v, Expected: %v", string(logData), expectedOutput)
	}

	exitData, err := os.ReadFile(exitCodeFilePath)
	if err != nil {
		t.Errorf("Error reading exitCode file: %v", err)
	}

	if string(exitData) != fmt.Sprint(0) {
		t.Errorf("Unexpected exitCode. Got: %v, Expected: %v", string(exitData), 0)
	}
}
