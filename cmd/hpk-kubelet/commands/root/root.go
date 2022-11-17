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

package root

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"os"
	"path"
	"time"

	"github.com/carv-ics-forth/hpk/cmd/hpk-kubelet/commands"
	"github.com/carv-ics-forth/hpk/compute"
	"github.com/carv-ics-forth/hpk/pkg/resourcemanager"
	"github.com/sirupsen/logrus"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"

	"github.com/carv-ics-forth/hpk/provider"
	"github.com/dimiro1/banner"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"github.com/virtual-kubelet/virtual-kubelet/node"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	kubeinformers "k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func logo() string {
	buf := bytes.NewBuffer(nil)

	banner.InitString(buf, true, true, `
{{ .AnsiColor.BrightRed }}
{{ .Title "HaPaKi" "" 4 }}
{{ .AnsiColor.BrightGreen }}
	`)

	return buf.String()
}

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
			if c.PodSyncWorkers == 0 {
				return errors.Errorf("pod sync workers must be greater than 0")
			}

			return runRootCommand(ctx, c)
		},
	}

	installFlags(cmd.Flags(), &c)
	return cmd
}

var DefaultLogger = zap.New(zap.UseDevMode(true))

func runRootCommand(ctx context.Context, c Opts) error {
	fmt.Println(logo())

	/*---------------------------------------------------
	 * Starting Kubernetes Client
	 *---------------------------------------------------*/
	DefaultLogger.Info("* Starting Kubernetes Client ....")

	// Config precedence
	//
	// * --kubeconfig flag pointing at a file
	//
	// * KUBECONFIG environment variable pointing at a file
	//
	// * In-cluster config if running in cluster
	//
	// * $HOME/.kube/config if exists.
	cfg, err := config.GetConfig()
	if err != nil {
		return errors.Wrapf(err, "unable to get kubeconfig")
	}

	/*-- fixme: replace the clientset with the most modern client --*/
	clientSet, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return errors.Wrapf(err, "unable to start kubernetes clientset")
	}

	modernClient, err := client.New(cfg, client.Options{})
	if err != nil {
		return errors.Wrapf(err, "unable to start kubernetes client")
	}

	u, err := url.Parse(cfg.Host)
	if err != nil {
		return errors.Wrapf(err, "failed to parse KUBERNETES_MASTER url")
	}

	compute.ClientSet = clientSet
	compute.K8SClient = modernClient
	compute.Environment.ContainerRegistry = c.ContainerRegistry

	compute.Environment.KubeServiceHost = u.Hostname()
	compute.Environment.KubeServicePort = u.Port()

	DefaultLogger.Info(" ... Done ...",
		"host", compute.Environment.KubeServiceHost,
		"port", compute.Environment.KubeServicePort,
		"registry", compute.Environment.ContainerRegistry,
	)

	/*---------------------------------------------------
	 * Discover Kubernetes DNS server
	 *---------------------------------------------------*/
	DefaultLogger.Info("* Discovering Kubernetes DNS Server")

	dnsEndpoint, err := clientSet.CoreV1().Endpoints("kube-system").Get(ctx, "kube-dns", metav1.GetOptions{})
	if err != nil {
		return errors.Wrapf(err, "unable to discover dns server")
	}

	if len(dnsEndpoint.Subsets) == 0 {
		return errors.Wrapf(err, "empty dns subsets")
	}

	if len(dnsEndpoint.Subsets[0].Addresses) == 0 {
		return errors.Wrapf(err, "empty dns addresses")
	}

	compute.Environment.KubeDNS = dnsEndpoint.Subsets[0].Addresses[0].IP

	DefaultLogger.Info(" ... Done ...", "dnsIP", compute.Environment.KubeDNS)

	/*---------------------------------------------------
	 * Load Kubernetes Informers
	 *---------------------------------------------------*/
	DefaultLogger.Info("* Starting Kubernetes Informers",
		"namespace", c.KubeNamespace,
		"crds", []string{
			"secrets", "configMap", "service", "serviceAccount",
		})

	// Create a shared informer factory for Kubernetes pods in the current namespace (if specified) and scheduled to the current node.
	podInformerFactory := kubeinformers.NewSharedInformerFactoryWithOptions(
		clientSet,
		c.InformerResyncPeriod,
		kubeinformers.WithNamespace(c.KubeNamespace),
		kubeinformers.WithTweakListOptions(func(options *metav1.ListOptions) {
			options.FieldSelector = fields.OneTermEqualSelector("spec.nodeName", c.NodeName).String()
		}))

	// Create another shared informer factory for Kubernetes secrets and configmaps (not subject to any selectors).
	podInformer := podInformerFactory.Core().V1().Pods()

	scmInformerFactory := kubeinformers.NewSharedInformerFactoryWithOptions(clientSet, c.InformerResyncPeriod)
	secretInformer := scmInformerFactory.Core().V1().Secrets()
	configMapInformer := scmInformerFactory.Core().V1().ConfigMaps()
	serviceInformer := scmInformerFactory.Core().V1().Services()
	serviceAccountInformer := scmInformerFactory.Core().V1().ServiceAccounts()

	if _, err := resourcemanager.NewResourceManager(
		podInformer.Lister(),
		secretInformer.Lister(),
		configMapInformer.Lister(),
		serviceInformer.Lister(),
		serviceAccountInformer.Lister(),
	); err != nil {
		return errors.Wrap(err, "could not create resource manager")
	}

	// Start the informers now, so the provider will get a functional resource manager.
	podInformerFactory.Start(ctx.Done())

	scmInformerFactory.Start(ctx.Done())

	DefaultLogger.Info(" ... Done ...")

	/*---------------------------------------------------
	 * Register the Provisioner of Virtual Nodes
	 *---------------------------------------------------*/
	DefaultLogger.Info("* Creating the Provisioner of Virtual Nodes")

	newProvider, err := provider.NewProvider(provider.InitConfig{
		NodeName:     c.NodeName,
		InternalIP:   envOr("VKUBELET_POD_IP", "127.0.0.1"),
		DaemonPort:   c.ListenAddr,
		BuildVersion: commands.BuildVersion,
	})
	if err != nil {
		return err
	}

	DefaultLogger.Info(" ... Done ...",
		"nodeName", newProvider.NodeName,
		"internalIP", newProvider.InternalIP,
		"daemonPort", newProvider.DaemonPort,
	)

	/*---------------------------------------------------
	 * Start an HTTPs server for serving metrics/logs
	 *---------------------------------------------------*/
	DefaultLogger.Info("* Initializing HTTP(s) server")
	{
		serverConfig := &apiServerConfig{
			CertPath:    os.Getenv("APISERVER_CERT_LOCATION"),
			KeyPath:     os.Getenv("APISERVER_KEY_LOCATION"),
			Addr:        fmt.Sprintf(":%d", c.ListenAddr),
			MetricsAddr: "",
		}

		cancelHTTP, err := setupHTTPServer(ctx, newProvider, serverConfig)
		if err != nil {
			return errors.Wrapf(err, "unable to start http server")
		}

		DefaultLogger.Info(" ... Done ...", "addr", serverConfig.Addr)

		defer cancelHTTP()
	}

	/*---------------------------------------------------
	 * Register a new Virtual Node
	 *---------------------------------------------------*/
	DefaultLogger.Info("* Creating a new Virtual Node")

	var taint *corev1.Taint
	if !c.DisableTaint {
		taint, err = getTaint(c)
		if err != nil {
			return err
		}
	}

	virtualNode := newProvider.CreateVirtualNode(ctx, c.NodeName, taint)

	// activate fs notifier
	nodeControllerOpts := []node.NodeControllerOpt{
		node.WithNodeStatusUpdateErrorHandler(func(ctx context.Context, err error) error {
			if !k8serrors.IsNotFound(err) {
				return err
			}

			DefaultLogger.V(0).Info("node not found")
			newNode := virtualNode.DeepCopy()
			newNode.ResourceVersion = ""

			if _, err := clientSet.CoreV1().Nodes().Create(ctx, newNode, metav1.CreateOptions{}); err != nil {
				return err
			}

			DefaultLogger.V(0).Info("created new node")
			return nil
		}),
	}

	if c.EnableNodeLease {
		leaseClient := clientSet.CoordinationV1().Leases(corev1.NamespaceNodeLease)
		nodeControllerOpts = append(nodeControllerOpts, node.WithNodeEnableLeaseV1(leaseClient, 0))
	}

	nodeController, err := node.NewNodeController(
		node.NaiveNodeProvider{},
		virtualNode,
		clientSet.CoreV1().Nodes(),
		nodeControllerOpts...,
	)
	if err != nil {
		return errors.Wrap(err, "cannot start node controller")
	}

	eb := record.NewBroadcaster()
	eb.StartLogging(logrus.Infof)

	DefaultLogger.Info(" ... Done ...",
		"nodeID", virtualNode.Spec.ProviderID,
		"taints", virtualNode.Spec.Taints)

	/*---------------------------------------------------
	 * Start the controller for the Virtual Node
	 *---------------------------------------------------*/
	DefaultLogger.Info("* Starting Virtual Node Controller")

	podController, err := node.NewPodController(node.PodControllerConfig{
		PodClient:         clientSet.CoreV1(),
		PodInformer:       podInformer,
		EventRecorder:     eb.NewRecorder(scheme.Scheme, corev1.EventSource{Component: path.Join(virtualNode.Name, "pod-controller")}),
		Provider:          newProvider,
		SecretInformer:    secretInformer,
		ConfigMapInformer: configMapInformer,
		ServiceInformer:   serviceInformer,
	})
	if err != nil {
		return errors.Wrap(err, "error setting up pod controller")
	}

	go func() {
		if err := podController.Run(ctx, c.PodSyncWorkers); err != nil && errors.Cause(err) != context.Canceled {
			DefaultLogger.Error(err, "pod controller failed")
			os.Exit(-1)
		}
	}()

	if c.StartupTimeout > 0 {
		// If there is a startup timeout, it does two things:
		// 1. It causes the VirtualKubelet to shut down if we haven't gotten into an operational state in a time period
		// 2. It prevents node advertisement from happening until we're in an operational state
		err = waitFor(ctx, c.StartupTimeout, podController.Ready())
		if err != nil {
			return err
		}
	}

	return nodeController.Run(ctx)
}

func waitFor(ctx context.Context, time time.Duration, ready <-chan struct{}) error {
	ctx, cancel := context.WithTimeout(ctx, time)
	defer cancel()

	// Wait for the VK / PC close the ready channel, or time out and return
	DefaultLogger.Info("Waiting for pod controller / VK to be ready")

	select {
	case <-ready:
		return nil
	case <-ctx.Done():
		return errors.Wrap(ctx.Err(), "Error while starting up VK")
	}
}

func envOr(name, alt string) string {
	if v, ok := os.LookupEnv(name); ok {
		return v
	}

	return alt
}
