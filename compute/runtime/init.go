package runtime

import (
	"os"

	"github.com/carv-ics-forth/hpk/compute"
	"github.com/carv-ics-forth/hpk/compute/endpoint"
	"github.com/carv-ics-forth/hpk/compute/image"
	"github.com/pkg/errors"
)

var (
	// DefaultPauseImage is an actionable object of the pause container.
	DefaultPauseImage *image.Image
)

func Initialize() error {
	compute.HPK = endpoint.HPK(compute.Environment.WorkingDirectory)

	// create the ~/.hpk directory, if it does not exist.
	if err := os.MkdirAll(compute.HPK.String(), endpoint.PodGlobalDirectoryPermissions); err != nil {
		return errors.Wrapf(err, "Failed to create RuntimeDir '%s'", compute.HPK.String())
	}

	// create the ~/.hpk/image directory, if it does not exist.
	if err := os.MkdirAll(compute.HPK.ImageDir(), endpoint.PodGlobalDirectoryPermissions); err != nil {
		return errors.Wrapf(err, "Failed to create ImageDir '%s'", compute.HPK.ImageDir())
	}

	// create the ~/.hpk/corrupted directory, if it does not exist.
	if err := os.MkdirAll(compute.HPK.CorruptedDir(), endpoint.PodGlobalDirectoryPermissions); err != nil {
		return errors.Wrapf(err, "Failed to create CorruptedDir '%s'", compute.HPK.CorruptedDir())
	}

	img, err := image.Pull(compute.HPK.ImageDir(), image.Docker, image.PauseImage)
	if err != nil {
		return errors.Wrapf(err, "failed to get pause container image")
	}

	DefaultPauseImage = img

	compute.DefaultLogger.Info("Runtime info",
		"WorkingDirectory", compute.HPK.String(),
		"PauseImagePath", DefaultPauseImage,
	)

	return nil
}
