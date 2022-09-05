/*
Copyright 2022 ICS-FORTH.
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package job

import (
	"github.com/carv-ics-forth/knoc/api"
	"github.com/carv-ics-forth/knoc/door"
	"github.com/carv-ics-forth/knoc/pkg/ui"
	"github.com/spf13/cobra"
)

type JobSubmitOptions struct{}

func PopulateJobSubmitFlags(cmd *cobra.Command, options *JobSubmitOptions) {
	_ = cmd
	_ = options
	// cmd.Flags().StringVar(&options.CPUQuota, "cpu", "", "set quotas for the total CPUs (e.g, 0.5) that can be used by all Pods running in the test.")
	// cmd.Flags().StringVar(&options.MemoryQuota, "memory", "", "set quotas for the total Memory (e.g, 100Mi) that can be used by all Pods running in the test.")
}

func NewSubmitJobCmd() *cobra.Command {
	var options JobSubmitOptions

	cmd := &cobra.Command{
		Use:     "job <Action> <ContainerManifest>  ",
		Aliases: []string{"j"},
		Short:   "Submit a new job",
		Long:    `.. Kati ...`,
		Example: `# Submit multiple workflows from files:
  kubectl frisbee submit test my-wf.yaml
# Submit and wait for completion:
  kubectl frisbee submit test --wait my-wf.yaml
# Submit and watch until completion:
  kubectl frisbee submit test --watch my-wf.yaml
# Submit and tail logs until completion:
  kubectl frisbee submit test --log my-wf.yaml
`,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) < 2 {
				ui.Failf("Pass Action and Container's Manifest")
			}

			return nil
		},

		Run: func(cmd *cobra.Command, args []string) {
			ui.Logo()

			action := args[0]
			manifest := args[1]

			dc, err := door.ImportContainerb64Json(manifest)
			ui.ExitOnError("Import Container Manifest", err)

			switch api.Operation(action) {
			case api.SUBMIT:
				err := door.CreateContainer(dc)
				ui.ExitOnError("Create container", err)
			case api.DELETE:
				err := door.DeleteContainer(dc)
				ui.ExitOnError("Delete container", err)
			}
		},
	}

	PopulateJobSubmitFlags(cmd, &options)

	return cmd
}
