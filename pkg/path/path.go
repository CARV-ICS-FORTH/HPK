// Copyright © 2022 FORTH-ICS
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

package path

import (
	"os"
	"os/exec"

	"github.com/sirupsen/logrus"
)

// GetPathOrDie searches for an executable named binary in the directories named by the PATH environment variable
// and returns the binary's path, otherwise panics
func GetPathOrDie(executableName string) string {
	// find the executable
	path, err := exec.LookPath(executableName)
	if err != nil {
		panic(err)
	}

	// check that we can access the executable (permissions, etc etc)
	if _, err := os.Stat(path); err != nil {
		panic(err)
	}

	logrus.Infof("Found executable '%s' -> '%s'", executableName, path)

	return path
}
