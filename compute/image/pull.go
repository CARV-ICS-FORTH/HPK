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

package image

import (
	"os"
	"strings"

	"github.com/carv-ics-forth/hpk/compute"
	"github.com/carv-ics-forth/hpk/pkg/process"
	"github.com/pkg/errors"
)

func Pull(imageDir string, transport Transport, imageName string) (*Image, error) {
	// Remove the digest form the image, because Singularity fails with
	// "Docker references with both a tag and digest are currently not supported".
	imageName = strings.Split(imageName, "@")[0]

	img := &Image{
		Filepath: imageDir + ParseImageName(imageName),
	}

	// check if image exists
	file, err := os.Stat(img.Filepath)
	if err == nil {
		if file.Mode().IsRegular() {
			return img, nil
		}

		return nil, errors.Errorf("imagePath '%s' is not a regular fie", img.Filepath)
	}

	// otherwise, download a fresh copy
	if _, err := process.Execute(compute.Environment.ApptainerBin, "pull", "--dir", imageDir, transport.Wrap(imageName)); err != nil {
		return nil, errors.Wrapf(err, "downloading has failed")
	}

	compute.DefaultLogger.Info(" * Download completed", "image", imageName, "path", img.Filepath)

	return img, nil
}

func ParseImageName(rawImageName string) string {
	// filter host
	var imageName string

	hostImage := strings.Split(rawImageName, "/")
	switch {
	case len(hostImage) == 1:
		imageName = hostImage[0]
	case len(hostImage) > 1:
		imageName = hostImage[len(hostImage)-1]
	default:
		panic("invalid name: " + rawImageName)
	}

	// filter version
	imageNameVersion := strings.Split(imageName, ":")
	switch {
	case len(imageNameVersion) == 1:
		name := imageNameVersion[0]
		version := "latest"

		return "/" + name + "_" + version + ".sif"
	case len(imageNameVersion) == 2:
		name := imageNameVersion[0]
		version := imageNameVersion[1]

		return "/" + name + "_" + version + ".sif"

	default:
		// keep the tag (version), but ignore the digest (sha256)
		// registry.k8s.io/ingress-nginx/kube-webhook-certgen:v20230407@sha256:543c40fd093964bc9ab509d3e791f9989963021f1e9e4c9c7b6700b02bfb227b
		imageNameVersionDigest := strings.Split(imageName, "@")
		digest := imageNameVersionDigest[1]
		_ = digest

		imageNameVersion = strings.Split(imageNameVersionDigest[0], ":")
		name := imageNameVersion[0]
		version := imageNameVersion[1]

		return "/" + name + "_" + version + ".sif"
	}
}
