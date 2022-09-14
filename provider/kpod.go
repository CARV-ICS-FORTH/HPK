package provider

import (
	"context"
	"fmt"
	"github.com/carv-ics-forth/knoc/api"
	"github.com/fsnotify/fsnotify"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var escapeScripts = regexp.MustCompile(`(--[A-Za-z0-9\-]+=)(\$[{\(][A-Za-z0-9_]+[\)\}])`)

func NewKPod(provider *Provider, pod *corev1.Pod, cRg string) *KPod {
	return &KPod{
		Provider:          provider,
		Pod:               pod,
		Envs:              nil,
		Mounts:            nil,
		Commands:          nil,
		CommandArguments:  nil,
		containerRegistry: cRg,
	}
}

type KPod struct {
	*Provider

	Pod *corev1.Pod

	Envs             []string
	Mounts           []string
	Commands         []string
	CommandArguments []string

	containerRegistry string
}

func (kpod *KPod) JobName(container *corev1.Container) string {
	return kpod.Pod.Name + "/" + container.Name
}

func (kpod *KPod) TmpRootDir() string {
	return filepath.Join(api.TemporaryDir, kpod.Pod.Name)
}

func (kpod *KPod) PodRuntimeRootDir() string {
	return filepath.Join(api.RuntimeDir, kpod.Pod.Name)
}

func (kpod *KPod) ScriptFilePath(container *corev1.Container) string {
	return filepath.Join(kpod.TmpRootDir(), container.Name+".sh")
}

func (kpod *KPod) StdOutputFilePath(container *corev1.Container) string {
	return filepath.Join(kpod.PodRuntimeRootDir(), container.Name+".stdout")
}

func (kpod *KPod) StdErrorFilePath(container *corev1.Container) string {
	return filepath.Join(kpod.PodRuntimeRootDir(), container.Name+".stderr")
}

func (kpod *KPod) ExitCodeFilePath(container *corev1.Container) string {
	return filepath.Join(kpod.PodRuntimeRootDir(), container.Name+".exitCode")
}

func (kpod *KPod) JobIDFilePath(container *corev1.Container) string {
	return filepath.Join(kpod.PodRuntimeRootDir(), container.Name+".jid")
}

func (kpod *KPod) ContainerRegistry() string {
	return kpod.containerRegistry
}

func (kpod *KPod) CreateTemporaryDirectories() error {
	if err := os.MkdirAll(kpod.TmpRootDir(), os.ModePerm); err != nil {
		return errors.Wrapf(err, "Cant create pod directory '%s'", kpod.TmpRootDir())
	}

	return nil
}

func (kpod *KPod) CreateRuntimeDirectories() error {
	if err := os.MkdirAll(kpod.PodRuntimeRootDir(), os.ModePerm); err != nil {
		return errors.Wrapf(err, "Cant create pod directory '%s'", kpod.PodRuntimeRootDir())
	}

	return nil
}

func (kpod *KPod) GenerateMountPaths(mount corev1.VolumeMount) string {
	return fmt.Sprintf("%s/%s/%s:%s", api.RuntimeDir, kpod.Pod.GetName(), mount.Name, mount.MountPath)
}

func (kpod *KPod) ExecuteOperation(ctx context.Context, mode api.Operation) error {
	switch mode {
	case api.SUBMIT:
		var prevJobID *int
		prevJobID = new(int)
		*prevJobID = -1
		lastInitContainerJobID := -1

		for index := range kpod.Pod.Spec.InitContainers {
			if err := kpod.createContainer(ctx, &kpod.Pod.Spec.InitContainers[index], prevJobID); err != nil {
				return errors.Wrapf(err, "Could not create container from door")
			}

			// We're keeping the last init container jobID to give it to the main Containers as a dependency,
			// since we don't want sequential execution on main Containers
			lastInitContainerJobID = *prevJobID
		}

		for index := range kpod.Pod.Spec.Containers {
			*prevJobID = lastInitContainerJobID
			if err := kpod.createContainer(ctx, &kpod.Pod.Spec.Containers[index], prevJobID); err != nil {
				return errors.Wrapf(err, "Could not create container from door")
			}
		}
	case api.DELETE:
		/* ... */
	}

	return nil
}

func (kpod *KPod) createContainer(ctx context.Context, container *corev1.Container, prevJobID *int) error {
	logrus.Info("Create container")
	/* Prepare the data environment */
	if err := kpod.PrepareContainerData(container); err != nil {
		return errors.Wrap(err, "cannot prepare data environment")
	}

	/*
		local singularity image files currently unavailable
	*/
	/*
		if strings.HasPrefix(c.Image, "/") {

			if imageURI, ok := c.GetObjectMeta().GetAnnotations()["slurm-job.knoc.io/image-root"]; ok {
				logrus.Debugln(imageURI)
				image = imageURI + c.Image
			} else {
				return errors.Errorf("image-uri annotation not specified for path in remote filesystem")
			}
		}
	*/
	singularityCommand := append([]string{kpod.Provider.HPC.SingularityPath(), "exec"}, kpod.Envs...)
	singularityCommand = append(singularityCommand, kpod.Mounts...)
	singularityCommand = append(singularityCommand, kpod.containerRegistry+container.Image)
	singularityCommand = append(singularityCommand, kpod.Commands...)
	singularityCommand = append(singularityCommand, kpod.CommandArguments...)

	path, err := kpod.produceSlurmScript(container, *prevJobID, singularityCommand)
	if err != nil {
		return errors.Wrap(err, "cannot generate slurm script")
	}

	out, err := kpod.Provider.HPC.SBatchFromFile(path)
	if err != nil {
		return errors.Wrapf(err, "Can't submit sbatch script")
	}

	// if sbatch script is successfully submitted check for job id
	jid, err := kpod.handleJobID(container, out)
	if err != nil {
		return errors.Wrapf(err, "handleJobID")
	}

	*prevJobID = jid

	return kpod.WatchPodDirectory(ctx)
}

func (kpod *KPod) PrepareContainerData(container *corev1.Container) error {

	preparePendingEvaluations := func(flags []string) []string {
		cleanedFlagValues := make([]string, 0, len(flags))

		for _, flag := range flags {
			flagValurWithEvaluationPending := escapeScripts.FindStringSubmatch(flag)
			if len(flagValurWithEvaluationPending) > 0 {
				param := flagValurWithEvaluationPending[1]
				value := flagValurWithEvaluationPending[2]
				cleanedFlagValues = append(cleanedFlagValues, param+"'"+value+"'")
			} else {
				cleanedFlagValues = append(cleanedFlagValues, flag)
			}
		}
		return cleanedFlagValues
	}

	prepareEnvironment := func(container *corev1.Container) []string {
		envArgs := make([]string, 0, len(container.Env))

		for _, envVar := range container.Env {
			envArgs = append(envArgs, envVar.Name+"="+envVar.Value)
		}

		// in case there is an evaluation pending inside the container arguments
		return []string{"--env", strings.Join(envArgs, ",")}
	}

	kpod.Envs = prepareEnvironment(container)
	kpod.Commands = preparePendingEvaluations(container.Command)
	kpod.CommandArguments = preparePendingEvaluations(container.Args)
	kpod.Mounts = kpod.prepareMounts(container)

	return nil
}

func (kpod *KPod) prepareMounts(container *corev1.Container) []string {
	mountArgs := make([]string, 0, len(container.VolumeMounts))

	for _, mountVar := range container.VolumeMounts {
		mountArgs = append(mountArgs, kpod.GenerateMountPaths(mountVar))
	}

	return []string{"--bind", strings.Join(mountArgs, ",")}
}

func (kpod *KPod) produceSlurmScript(container *corev1.Container, prevJobID int, command []string) (string, error) {
	sbatchRelativePath := filepath.Join(api.TemporaryDir, kpod.Pod.GetName(), container.Name+".sh")

	var sbatchFlagsFromArgo []string
	var sbatchFlagsAsString = ""

	if err := os.MkdirAll(filepath.Join(api.TemporaryDir, kpod.Pod.GetName()), api.SecretPodData); err != nil {
		return "", errors.Wrapf(err, "Could not create '%s' directory", filepath.Join(api.TemporaryDir, kpod.Pod.GetName()))
	}

	if slurmFlags, ok := kpod.Pod.GetAnnotations()["slurm-job.knoc.io/flags"]; ok {
		sbatchFlagsFromArgo = strings.Split(slurmFlags, " ")
	}

	for _, slurmFlag := range sbatchFlagsFromArgo {
		sbatchFlagsAsString += "\n#SBATCH " + slurmFlag
	}
	// SLURM_JOB_DEPENDENCY ensures sequential execution of init containers and main containers
	if prevJobID != -1 {
		sbatchFlagsAsString += "\n#SBATCH --dependency afterok:" + string(rune(prevJobID))
	}

	if mpiFlags, ok := kpod.Pod.GetAnnotations()["slurm-job.knoc.io/mpi-flags"]; ok {
		if mpiFlags != "true" {
			mpi := append([]string{kpod.Provider.HPC.MpiexecPath(), "-np", "$SLURM_NTASKS"}, strings.Split(mpiFlags, " ")...)
			command = append(mpi, command...)
		}
	}

	if err := kpod.writeSlurmScriptToFile(sbatchFlagsAsString, container, command); err != nil {
		return "", errors.Wrap(err, "Can't produce sbatch script in file")
	}

	return sbatchRelativePath, nil
}

func (kpod *KPod) writeSlurmScriptToFile(sbatchFlagsAsString string, c *corev1.Container, commandArray []string) error {

	ssfPath := kpod.ScriptFilePath(c)

	scriptFile, err := os.Create(ssfPath)
	if err != nil {
		return errors.Wrapf(err, "Cant create slurm_script '%s'", ssfPath)
	}

	finalScriptData := kpod.Provider.HPC.SbatchMacros(kpod.JobName(c), sbatchFlagsAsString) + "\n" +
		strings.Join(commandArray, " ") + " >> " + kpod.StdOutputFilePath(c) +
		" 2>> " + kpod.StdErrorFilePath(c) +
		"\n echo $? > " + kpod.ExitCodeFilePath(c)

	if _, err = scriptFile.WriteString(finalScriptData); err != nil {
		return errors.Wrapf(err, "Can't write sbatch script in file '%s'", ssfPath)
	}

	if err := scriptFile.Close(); err != nil {
		return errors.Wrap(err, "Close")
	}

	return nil
}

func (kpod *KPod) handleJobID(container *corev1.Container, output string) (int, error) {
	r := regexp.MustCompile(`Submitted batch job (?P<jid>\d+)`)
	jid := r.FindStringSubmatch(output)

	f, err := os.Create(kpod.JobIDFilePath(container))
	if err != nil {
		return -1, errors.Wrap(err, "Cant create jid_file")
	}

	if _, err := f.WriteString(jid[1]); err != nil {
		return -1, errors.Wrap(err, "Cant write jid_file")
	}
	intJobId, err := strconv.Atoi(jid[1])
	if err != nil {
		return 0, errors.Wrap(err, "Cant convert jid as integer. Parsed irregular output!")
	}
	return intJobId, f.Close()
}

func (kpod *KPod) WatchPodDirectory(ctx context.Context) error {
	// Create new watcher.
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return errors.Wrap(err, "cannot create a new watcher")
	}

	// Add a path.
	if err := watcher.Add(kpod.PodRuntimeRootDir()); err != nil {
		return errors.Wrap(err, "add path to watcher has failed")
	}

	// Start listening for events.
	go func() {
		defer watcher.Close()

		for {
			select {
			case <-ctx.Done():
				return

			case event, ok := <-watcher.Events:
				if !ok {
					return
				}

				log.Println("event:", event)

				switch event.Op {
				case fsnotify.Write:
					log.Println("Modified file:", event.Name)

				case fsnotify.Remove:
					log.Println("Deleted file:", event.Name)

				case fsnotify.Create:
					log.Println("Created file:", event.Name)

				case fsnotify.Chmod:
					log.Println("Chmoded file:", event.Name)

				case fsnotify.Rename:
					log.Println("Renamed file:", event.Name)
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}

				log.Println("error:", err)
			}
		}
	}()

	return nil
}
