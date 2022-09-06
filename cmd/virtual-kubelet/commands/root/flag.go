// Copyright © 2021 FORTH-ICS
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
// limitations under the License.package main

package root

import (
	"os"

	"github.com/spf13/pflag"
)

func installFlags(flags *pflag.FlagSet, c *Opts) {
	flags.StringVar(&c.KubeConfigPath, "kubeconfig", c.KubeConfigPath, "kube config file to use for connecting to the Kubernetes API server")
	flags.StringVar(&c.KubeNamespace, "namespace", c.KubeNamespace, "kubernetes namespace (default is 'all')")
	flags.StringVar(&c.NodeName, "nodename", c.NodeName, "kubernetes node name")
	flags.StringVar(&c.OperatingSystem, "os", c.OperatingSystem, "Operating System (Linux/Windows)")

	flags.StringVar(&c.ProviderConfigPath, "provider-config", c.ProviderConfigPath, "HPC provider configuration file")
	flags.StringVar(&c.MetricsAddr, "metrics-addr", c.MetricsAddr, "address to listen for metrics/stats requests")

	flags.IntVar(&c.PodSyncWorkers, "pod-sync-workers", c.PodSyncWorkers, `set the number of pod synchronization workers`)
	flags.BoolVar(&c.EnableNodeLease, "enable-node-lease", c.EnableNodeLease, `use node leases (1.13) for node heartbeats`)

	flags.DurationVar(&c.InformerResyncPeriod, "full-resync-period", c.InformerResyncPeriod, "how often to perform a full resync of pods between kubernetes and the provider")
	flags.DurationVar(&c.StartupTimeout, "startup-timeout", c.StartupTimeout, "How long to wait for the virtual-kubelet to start")
}

func getEnv(key, defaultValue string) string {
	value, found := os.LookupEnv(key)
	if found {
		return value
	}
	return defaultValue
}
