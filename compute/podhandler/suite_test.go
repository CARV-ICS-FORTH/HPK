package podhandler_test

import (
	"os"
	"testing"

	"github.com/carv-ics-forth/hpk/compute"
	"github.com/carv-ics-forth/hpk/compute/runtime"
)

func TestMain(m *testing.M) {
	tmpDir, err := os.MkdirTemp("/tmp", "randomuser")
	if err != nil {
		panic(err)
	}

	setup(tmpDir)
	code := m.Run()
	shutdown(tmpDir)
	os.Exit(code)
}

func setup(tmpDir string) {
	compute.Environment = compute.HostEnvironment{
		KubeMasterHost:    "",
		ContainerRegistry: "",
		ApptainerBin:      "singularity",
		EnableCgroupV2:    false,
		WorkingDirectory:  tmpDir,
		KubeDNS:           "",
	}

	if err := runtime.Initialize(); err != nil {
		panic(err)
	}
}

func shutdown(tmpDir string) {
	os.RemoveAll(tmpDir)
}
