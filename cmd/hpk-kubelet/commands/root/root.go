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
	"net/http"
	"net/url"

	"github.com/carv-ics-forth/hpk/cmd/hpk-kubelet/commands"
	"github.com/carv-ics-forth/hpk/compute"
	"github.com/carv-ics-forth/hpk/pkg/resourcemanager"
	"github.com/sirupsen/logrus"
	"github.com/virtual-kubelet/virtual-kubelet/node/api"
	k8errors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"

	"github.com/carv-ics-forth/hpk/provider"
	"github.com/dimiro1/banner"
	"github.com/hashicorp/go-multierror"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"github.com/virtual-kubelet/virtual-kubelet/node"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	kubeinformers "k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func logo() string {
	buf := bytes.NewBuffer(nil)

	banner.InitString(buf, true, true, `
{{ .AnsiColor.BrightGreen }}
{{ .Title "HaPaKi" "" 4 }}
{{ .AnsiColor.Default }}
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
			var merr *multierror.Error

			/*---------------------------------------------------
			 * Sanitize Input Params
			 *---------------------------------------------------*/
			if c.PodSyncWorkers == 0 {
				merr = multierror.Append(merr, errors.New("pod sync workers must be greater than 0"))
			}

			if c.KubeletAddress == "" {
				merr = multierror.Append(merr, errors.Errorf("empty kubelet address. Use flags or set %s", EnvKubeletAddress))
			}

			if c.K8sAPICertFilepath == "" {
				merr = multierror.Append(merr, errors.Errorf("empty certificate path. Use flags or set %s", EnvAPICertLocation))
			}

			if c.K8sAPIKeyFilepath == "" {
				merr = multierror.Append(merr, errors.Errorf("empty key path. Use flags or set %s", EnvAPIKeyLocation))
			}

			if merr.ErrorOrNil() != nil {
				return merr.ErrorOrNil()
			}

			/*---------------------------------------------------
			 * Start the execution
			 *---------------------------------------------------*/
			return runRootCommand(ctx, c)
		},
	}

	installFlags(cmd.Flags(), &c)
	return cmd
}

var DefaultLogger = zap.New(zap.UseDevMode(true))

func runRootCommand(parentCtx context.Context, c Opts) error {
	/*
		https://medium.com/microsoftazure/virtual-kubelet-turns-1-0-deep-dive-b64056061b18
	*/

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
	clientset, err := kubernetes.NewForConfig(cfg)
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

	compute.ClientSet = clientset
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
	{
		dnsEndpoint, err := clientset.CoreV1().Endpoints("kube-system").Get(parentCtx, "kube-dns", metav1.GetOptions{})
		if err != nil && !errors.Is(err, context.Canceled) {
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
	}

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
		clientset,
		c.InformerResyncPeriod,
		kubeinformers.WithNamespace(c.KubeNamespace),
		kubeinformers.WithTweakListOptions(func(options *metav1.ListOptions) {
			options.FieldSelector = fields.OneTermEqualSelector("spec.nodeName", c.NodeName).String()
		}))

	// Create another shared informer factory for Kubernetes secrets and configmaps (not subject to any selectors).
	podInformer := podInformerFactory.Core().V1().Pods()

	scmInformerFactory := kubeinformers.NewSharedInformerFactoryWithOptions(clientset, c.InformerResyncPeriod)
	secretInformer := scmInformerFactory.Core().V1().Secrets()
	configMapInformer := scmInformerFactory.Core().V1().ConfigMaps()
	serviceInformer := scmInformerFactory.Core().V1().Services()
	serviceAccountInformer := scmInformerFactory.Core().V1().ServiceAccounts()

	{
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
		podInformerFactory.Start(parentCtx.Done())
		scmInformerFactory.Start(parentCtx.Done())

		DefaultLogger.Info(" ... Done ...")
	}

	/*---------------------------------------------------
	 * Register the Provisioner of Virtual Nodes
	 *---------------------------------------------------*/
	DefaultLogger.Info("* Creating the Provisioner of Virtual Nodes")

	hpkProvider, err := provider.NewProvider(provider.InitConfig{
		NodeName:     c.NodeName,
		InternalIP:   c.KubeletAddress,
		DaemonPort:   c.KubeletPort,
		BuildVersion: commands.BuildVersion,
	})
	if err != nil {
		return err
	}

	DefaultLogger.Info(" ... Done ...",
		"nodeName", hpkProvider.NodeName,
		"internalIP", hpkProvider.InternalIP,
		"daemonPort", hpkProvider.DaemonPort,
	)

	/*---------------------------------------------------
	 * Start an HTTPs server for serving metrics/logs
	 *---------------------------------------------------*/
	DefaultLogger.Info("* Initializing HTTP(s) server")
	{
		mux := http.NewServeMux()

		api.AttachPodRoutes(api.PodHandlerConfig{
			RunInContainer:   hpkProvider.RunInContainer,
			GetContainerLogs: hpkProvider.GetContainerLogs,
			GetPods:          hpkProvider.GetPods,
			// GetPodsFromKubernetes: func(context.Context) ([]*corev1.Pod, error) {
			//	return clientset.CoreV1().Pods(c.KubeNamespace).List(ctx, labels.Everything())
			// },
			// GetStatsSummary:       hpkProvider.GetStatsSummary,
			// StreamIdleTimeout:     0,
			// StreamCreationTimeout: 0,
		}, mux, true)

		allAddr := fmt.Sprintf(":%d", c.KubeletPort)
		advertisedAddr := fmt.Sprintf("%s:%d", c.KubeletAddress, c.KubeletPort)

		go func() {
			if err := http.ListenAndServeTLS(
				allAddr,
				c.K8sAPICertFilepath,
				c.K8sAPIKeyFilepath,
				mux,
			); err != nil && !errors.Is(err, context.Canceled) {
				logrus.Fatal("API Server has failed. Err:", err)
				// handle error
			}
		}()

		DefaultLogger.Info("... Done ...",
			"address", advertisedAddr,
			"cert", c.K8sAPICertFilepath,
			"key", c.K8sAPIKeyFilepath,
		)
	}

	/*---------------------------------------------------
	 * Create a new Pod Controller
	 *---------------------------------------------------*/
	DefaultLogger.Info("* Creating Pod Controller")
	{
		eb := record.NewBroadcaster()
		eb.StartLogging(logrus.Infof)

		pc, err := node.NewPodController(node.PodControllerConfig{
			PodClient:         clientset.CoreV1(),
			PodInformer:       podInformer,
			EventRecorder:     eb.NewRecorder(scheme.Scheme, corev1.EventSource{Component: "hpk-controller"}),
			Provider:          hpkProvider,
			SecretInformer:    secretInformer,
			ConfigMapInformer: configMapInformer,
			ServiceInformer:   serviceInformer,
		})
		if err != nil {
			return err
		}

		var pcCtx context.Context

		// If there is a startup timeout, it does two things:
		// 1. It causes the VK to shut down if we haven't gotten into an operational state in a time period
		// 2. It prevents node advertisement from happening until we're in an operational state
		if c.StartupTimeout > 0 {
			tCtx, pcCancel := context.WithTimeout(parentCtx, c.StartupTimeout)
			defer pcCancel()

			pcCtx = tCtx
		}

		go func() {
			if err := pc.Run(pcCtx, c.PodSyncWorkers); err != nil && !errors.Is(err, context.Canceled) {
				DefaultLogger.Error(err, "Pod Controller Has failed")
				// handle error
			}
		}()

		// wait to start the node until the pod controller is ready
		select {
		case <-pc.Ready():
		case <-parentCtx.Done():
			DefaultLogger.Info("... Aborted before initialization is complete ....")

			return nil
		case <-pcCtx.Done():
			return errors.New("timed out waiting for pod controller to be ready")
		}
	}

	/*---------------------------------------------------
	 * Create Virtual Node Controller
	 *---------------------------------------------------*/
	DefaultLogger.Info("* Creating Node Controller")
	{
		var taint *corev1.Taint
		if !c.DisableTaint {
			taint, err = getTaint(c)
			if err != nil {
				return err
			}
		}

		nc, err := node.NewNodeController(
			&node.NaiveNodeProvider{},
			hpkProvider.CreateVirtualNode(parentCtx, c.NodeName, taint),
			clientset.CoreV1().Nodes(),
			node.WithNodeEnableLeaseV1(clientset.CoordinationV1().Leases(corev1.NamespaceNodeLease), 0),
			node.WithNodeStatusUpdateErrorHandler(func(ctx context.Context, err error) error {
				if !k8errors.IsNotFound(err) {
					return err
				}

				DefaultLogger.Info("Node not found")
				return nil
			}),
		)
		if err != nil {
			return err
		}

		go func() {
			if err := nc.Run(parentCtx); err != nil && err != context.Canceled {
				DefaultLogger.Error(err, "NodeController has failed")

				// handle error
			}
		}()

		// Wait for node controller to become ready
		<-nc.Ready()

		// wait for as long the app is running
		<-nc.Done()

		DefaultLogger.Info("... HPK has been gracefully terminated ....")
	}

	return nil
}
