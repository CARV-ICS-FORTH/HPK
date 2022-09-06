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
	"context"
	"os"
	"path"
	"time"

	"github.com/carv-ics-forth/knoc/provider"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/virtual-kubelet/virtual-kubelet/errdefs"
	"github.com/virtual-kubelet/virtual-kubelet/node"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	kubeinformers "k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

// NewCommand creates a new top-level command.
// This command is used to start the virtual-kubelet daemon
func NewCommand(ctx context.Context, name string, c Opts) *cobra.Command {
	cmd := &cobra.Command{
		Use:   name,
		Short: name + " provides a virtual kubelet interface for your kubernetes cluster.",
		Long: name + ` implements the Kubelet interface with a pluggable
backend implementation allowing users to create kubernetes nodes without running the kubelet.
This allows users to schedule kubernetes workloads on nodes that aren't running Kubernetes.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRootCommand(ctx, c)
		},
	}

	installFlags(cmd.Flags(), &c)
	return cmd
}

func runRootCommand(parentCtx context.Context, c Opts) error {

	log := zap.New(zap.UseDevMode(true)).WithValues(
		"operatingSystem", c.OperatingSystem,
		"node", c.NodeName,
		"watchedNamespace", c.KubeNamespace,
	)

	ctx, cancel := context.WithCancel(parentCtx)
	defer cancel()

	if c.PodSyncWorkers == 0 {
		return errdefs.InvalidInput("pod sync workers must be greater than 0")
	}

	client, err := newClient(c.KubeConfigPath)
	if err != nil {
		return err
	}

	// Create a shared informer factory for Kubernetes pods in the current namespace (if specified) and scheduled to the current node.
	podInformerFactory := kubeinformers.NewSharedInformerFactoryWithOptions(
		client,
		c.InformerResyncPeriod,
		kubeinformers.WithNamespace(c.KubeNamespace),
		kubeinformers.WithTweakListOptions(func(options *metav1.ListOptions) {
			options.FieldSelector = fields.OneTermEqualSelector("spec.nodeName", c.NodeName).String()
		}))
	podInformer := podInformerFactory.Core().V1().Pods()

	// Create another shared informer factory for Kubernetes secrets and configmaps (not subject to any selectors).
	scmInformerFactory := kubeinformers.NewSharedInformerFactoryWithOptions(client, c.InformerResyncPeriod)
	// Create a secret informer and a config map informer so we can pass their listers to the resource manager.
	secretInformer := scmInformerFactory.Core().V1().Secrets()
	configMapInformer := scmInformerFactory.Core().V1().ConfigMaps()
	serviceInformer := scmInformerFactory.Core().V1().Services()

	go podInformerFactory.Start(ctx.Done())
	go scmInformerFactory.Start(ctx.Done())

	apiConfig, err := getAPIConfig(c)
	if err != nil {
		return err
	}

	p, err := provider.NewProvider(provider.InitConfig{
		ConfigPath:      c.ProviderConfigPath,
		NodeName:        c.NodeName,
		OperatingSystem: c.OperatingSystem,
		InternalIP:      os.Getenv("VKUBELET_POD_IP"),
		DaemonPort:      c.ListenPort,
	})

	if err != nil {
		return err
	}

	pNode := p.CreateVirtualNode(ctx, c.NodeName, c.Version)

	nodeControllerOpts := []node.NodeControllerOpt{
		node.WithNodeStatusUpdateErrorHandler(func(ctx context.Context, err error) error {
			if !k8serrors.IsNotFound(err) {
				return err
			}

			log.V(0).Info("node not found")
			newNode := pNode.DeepCopy()
			newNode.ResourceVersion = ""

			_, err = client.CoreV1().Nodes().Create(ctx, newNode, metav1.CreateOptions{})
			if err != nil {
				return err
			}

			log.V(0).Info("created new node")
			return nil
		}),
	}

	if c.EnableNodeLease {
		leaseClient := client.CoordinationV1().Leases(corev1.NamespaceNodeLease)
		nodeControllerOpts = append(nodeControllerOpts, node.WithNodeEnableLeaseV1(leaseClient, 0))
	}

	nodeRunner, err := node.NewNodeController(
		node.NaiveNodeProvider{},
		pNode,
		client.CoreV1().Nodes(),
		nodeControllerOpts...,
	)
	if err != nil {
		log.Error(err, "cannot start node controller")
		os.Exit(-1)
	}

	eb := record.NewBroadcaster()
	eb.StartLogging(log.Info)
	eb.StartRecordingToSink(&corev1client.EventSinkImpl{Interface: client.CoreV1().Events(c.KubeNamespace)})

	pc, err := node.NewPodController(node.PodControllerConfig{
		PodClient:         client.CoreV1(),
		PodInformer:       podInformer,
		EventRecorder:     eb.NewRecorder(scheme.Scheme, corev1.EventSource{Component: path.Join(pNode.Name, "pod-controller")}),
		Provider:          p,
		SecretInformer:    secretInformer,
		ConfigMapInformer: configMapInformer,
		ServiceInformer:   serviceInformer,
	})
	if err != nil {
		return errors.Wrap(err, "error setting up pod controller")
	}

	cancelHTTP, err := setupHTTPServer(ctx, p, apiConfig)
	if err != nil {
		return err
	}
	defer cancelHTTP()

	go func() {
		if err := pc.Run(ctx, c.PodSyncWorkers); err != nil && errors.Cause(err) != context.Canceled {
			log.Error(err, "pod controller failed")
			os.Exit(-1)
		}
	}()

	if c.StartupTimeout > 0 {
		// If there is a startup timeout, it does two things:
		// 1. It causes the VK to shut down if we haven't gotten into an operational state in a time period
		// 2. It prevents node advertisement from happening until we're in an operational state
		err = waitFor(ctx, c.StartupTimeout, pc.Ready())
		if err != nil {
			return err
		}
	}

	go func() {
		if err := nodeRunner.Run(ctx); err != nil {
			log.Error(err, "node runner failed")
			os.Exit(-1)
		}
	}()

	log.Info("Initialized")

	<-ctx.Done()
	return nil
}

func waitFor(ctx context.Context, time time.Duration, ready <-chan struct{}) error {
	ctx, cancel := context.WithTimeout(ctx, time)
	defer cancel()

	// Wait for the VK / PC close the ready channel, or time out and return
	logrus.Warn("Waiting for pod controller / VK to be ready")

	select {
	case <-ready:
		return nil
	case <-ctx.Done():
		return errors.Wrap(ctx.Err(), "Error while starting up VK")
	}
}

func newClient(configPath string) (*kubernetes.Clientset, error) {

	/*
		mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
			Scheme: runtime.NewScheme(),
		})

	*/

	var config *rest.Config

	// Check if the kubeConfig file exists.
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		// Get the kubeconfig from the filepath.
		config, err = clientcmd.BuildConfigFromFlags("", configPath)
		if err != nil {
			return nil, errors.Wrap(err, "error building client config")
		}
	} else {
		// Set to in-cluster config.
		config, err = rest.InClusterConfig()
		if err != nil {
			return nil, errors.Wrap(err, "error building in cluster config")
		}
	}

	if masterURI := os.Getenv("MASTER_URI"); masterURI != "" {
		config.Host = masterURI
	}

	return kubernetes.NewForConfig(config)
}
