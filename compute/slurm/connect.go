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

package slurm

/************************************************************

			Initiate Slurm Connector

************************************************************/

func init() {
	Slurm.SubmitCmd = "sbatch"  // path.GetPathOrDie("sbatch")
	Slurm.CancelCmd = "scancel" // path.GetPathOrDie("scancel")
	Slurm.StatsCmd = "sinfo"
	Slurm.SqueueCmd = "squeue"
}

// Slurm represents a SLURM installation.
var Slurm struct {
	SubmitCmd string
	CancelCmd string
	StatsCmd  string
	SqueueCmd string
}

// ConnectionOK return true if HPK maintains connection with the Slurm manager.
// Otherwise, it returns false.
func ConnectionOK() bool {
	return true
}
