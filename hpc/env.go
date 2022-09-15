package hpc

import (
	"github.com/carv-ics-forth/knoc/api"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"os"
	"os/exec"
)

const (
	SBATCH      = "sbatch"
	SCANCEL     = "scancel"
	SINGULARITY = "singularity"
	MPIEXEC     = "mpiexec"
)

type HPCEnvironment struct {
	sbatchExecutablePath      string
	scancelExecutablePath     string
	singularityExecutablePath string
	mpiexecExecutablePath     string
	FSEventDispatcher         *FSEventDispatcher
}

// CheckExistenceOrDie searches for an executable named binary in the directories named by the PATH environment variable
// and returns the binary's path, otherwise panics
func CheckExistenceOrDie(binary string) string {
	if path, err := exec.LookPath(binary); err != nil {
		panic(errors.Wrapf(err, "Could not find '%s'", binary))
	} else {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			panic(errors.Wrapf(err, "'%s' doesn't exist", binary))
		} else {
			return path
		}
	}
}

func NewHPCEnvironment() *HPCEnvironment {
	return &HPCEnvironment{
		sbatchExecutablePath:      CheckExistenceOrDie(SBATCH),
		scancelExecutablePath:     CheckExistenceOrDie(SCANCEL),
		singularityExecutablePath: CheckExistenceOrDie(SINGULARITY),
		mpiexecExecutablePath:     CheckExistenceOrDie(MPIEXEC),

		FSEventDispatcher: NewFSEventDispatcher(Options{
			MaxWorkers:   api.DefaultMaxWorkers,
			MaxQueueSize: api.DefaultMaxQueueSize,
		}),
	}
}

func (hpc *HPCEnvironment) MpiexecPath() string {
	return hpc.mpiexecExecutablePath
}

func (hpc *HPCEnvironment) SingularityPath() string {
	return hpc.singularityExecutablePath
}

func (hpc *HPCEnvironment) Scancel(args string) (string, error) {
	output, err := exec.Command(hpc.scancelExecutablePath, args).Output()
	if err != nil {
		return "", errors.Wrap(err, "Could not run scancel")
	}

	return string(output), nil
}

func (hpc *HPCEnvironment) SBatchFromFile(path string) (string, error) {

	logrus.Warn("PATH:", hpc.sbatchExecutablePath, " cmd:", path)

	output, err := exec.Command(hpc.sbatchExecutablePath, path).Output()
	logrus.Warn("poutsakia ", string(output))

	if err != nil {
		return "", errors.Wrapf(err, "Could not run sbatch. out : '%s'", string(output))
	}

	return string(output), nil
}

func (hpc *HPCEnvironment) SbatchMacros(instanceName string, sbatchFlags string) string {
	return "#!/bin/bash" +
		"\n#SBATCH --job-name=" + instanceName +
		sbatchFlags +
		"\n. ~/.bash_profile" +
		"\npwd; hostname; date\n"
}
