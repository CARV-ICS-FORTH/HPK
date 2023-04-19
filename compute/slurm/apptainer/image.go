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

package apptainer

import (
	"strings"

	"github.com/carv-ics-forth/hpk/compute"
	"github.com/carv-ics-forth/hpk/pkg/process"
)

type Transport string

const Docker = Transport("docker://")

func imageFilePath(image string) string {
	// filter host
	var imageName string

	hostImage := strings.Split(image, "/")
	switch {
	case len(hostImage) == 1:
		imageName = hostImage[0]
	case len(hostImage) > 1:
		imageName = hostImage[len(hostImage)-1]
	default:
		panic("invalid name: " + image)
	}

	// filter version
	imageNameVersion := strings.Split(imageName, ":")
	switch {
	case len(imageNameVersion) == 1:
		return compute.ImageDir + "/" + imageNameVersion[0] + "_" + "latest" + ".sif"
	case len(imageNameVersion) == 2:
		return compute.ImageDir + "/" + imageNameVersion[0] + "_" + imageNameVersion[1] + ".sif"
	default:
		panic("invalid version: " + image)
	}
}

func PullImage(transport Transport, image string) (string, error) {
	// otherwise, download a fresh copy
	downloadcmd := []string{"pull", "--dir", compute.ImageDir, string(transport) + image}

	_, _ = process.Execute(compute.Environment.ApptainerBin, downloadcmd...)

	imagePath := imageFilePath(image)

	return imagePath, nil
}
