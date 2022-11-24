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
	"time"

	"github.com/carv-ics-forth/hpk/cmd/hpk/commands"
	"github.com/carv-ics-forth/hpk/compute"
	"github.com/carv-ics-forth/hpk/pkg/resourcemanager"
	"github.com/sirupsen/logrus"
	"github.com/virtual-kubelet/virtual-kubelet/node/api"
	"golang.org/x/time/rate"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
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
		Short: name + " run hpk",
		Long:  name + ` run a kubelet-alike daemon that allows to schedule kubernetes workloads on Slurm nodes`,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println(logo())

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

// https://medium.com/microsoftazure/virtual-kubelet-turns-1-0-deep-dive-b64056061b18
func runRootCommand(ctx context.Context, c Opts) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	/*---------------------------------------------------
	 * Setup a client to Kubernetes API
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
	restConfig, err := config.GetConfig()
	if err != nil {
		return errors.Wrapf(err, "unable to get kubeconfig")
	}

	k8sclientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return errors.Wrapf(err, "unable to start kubernetes k8sclientset")
	}

	k8sclient, err := client.New(restConfig, client.Options{})
	if err != nil {
		return errors.Wrapf(err, "unable to start kubernetes client")
	}

	compute.K8SClient = k8sclient
	compute.Environment.ContainerRegistry = c.ContainerRegistry

	DefaultLogger.Info(" ... Done ...",
		"KubernetesURL", restConfig.Host,
		"registry", compute.Environment.ContainerRegistry,
	)

	/*---------------------------------------------------
	 * Discover Kubernetes DNS server
	 *---------------------------------------------------*/
	DefaultLogger.Info("* Discovering Kubernetes DNS Server")
	{
		dnsEndpoint, err := k8sclientset.CoreV1().Endpoints("kube-system").Get(ctx, "kube-dns", metav1.GetOptions{})
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
	 * Create Informers for Kubernetes CRDs
	 *---------------------------------------------------*/
	DefaultLogger.Info("* Starting Kubernetes Informers",
		"namespace", c.KubeNamespace,
		"crds", []string{
			"pods", "secrets", "configMap", "service", "serviceAccount",
		})

	// Create a shared informer factory for Kubernetes Pods assigned to this Node.
	podInformerFactory := kubeinformers.NewSharedInformerFactoryWithOptions(
		k8sclientset,
		c.InformerResyncPeriod,
		kubeinformers.WithTweakListOptions(func(options *metav1.ListOptions) {
			options.FieldSelector = fields.OneTermEqualSelector("spec.nodeName", c.NodeName).String()
		}),
	)
	podInformer := podInformerFactory.Core().V1().Pods()

	// Create another shared informer factory for Kubernetes secrets and configmaps (not subject to any selectors).
	informerFactory := kubeinformers.NewSharedInformerFactoryWithOptions(k8sclientset, c.InformerResyncPeriod)
	secretInformer := informerFactory.Core().V1().Secrets()
	configMapInformer := informerFactory.Core().V1().ConfigMaps()
	serviceInformer := informerFactory.Core().V1().Services()
	serviceAccountInformer := informerFactory.Core().V1().ServiceAccounts()

	// Setup the known Pods related resources manager.
	if _, err := resourcemanager.NewResourceManager(
		podInformer.Lister(),
		secretInformer.Lister(),
		configMapInformer.Lister(),
		serviceInformer.Lister(),
		serviceAccountInformer.Lister(),
	); err != nil {
		return errors.Wrap(err, "could not create resource manager")
	}

	// Finally, start the informers.
	podInformerFactory.Start(ctx.Done())
	podInformerFactory.WaitForCacheSync(ctx.Done())
	informerFactory.Start(ctx.Done())
	informerFactory.WaitForCacheSync(ctx.Done())

	DefaultLogger.Info(" ... Done ...")

	/*---------------------------------------------------
	 * Register the Provisioner of Virtual Nodes
	 *---------------------------------------------------*/
	DefaultLogger.Info("* Creating the Provisioner of Virtual Nodes")

	virtualk8s, err := provider.NewVirtualK8S(provider.InitConfig{
		NodeName:     c.NodeName,
		InternalIP:   c.KubeletAddress,
		DaemonPort:   c.KubeletPort,
		BuildVersion: commands.BuildVersion,
	})
	if err != nil {
		return err
	}

	DefaultLogger.Info(" ... Done ...",
		"nodeName", virtualk8s.NodeName,
		"internalIP", virtualk8s.InternalIP,
		"daemonPort", virtualk8s.DaemonPort,
	)

	/*---------------------------------------------------
	 * Start an HTTPs server for serving metrics/logs
	 *---------------------------------------------------*/
	DefaultLogger.Info("* Initializing HTTP(s) server")
	{
		mux := http.NewServeMux()

		api.AttachPodRoutes(api.PodHandlerConfig{
			RunInContainer:   virtualk8s.RunInContainer,
			GetContainerLogs: virtualk8s.GetContainerLogs,
			GetPods:          virtualk8s.GetPods,
			// GetPodsFromKubernetes: func(context.Context) ([]*corev1.Pod, error) {
			//	return k8sclientset.CoreV1().Pods(c.KubeNamespace).List(ctx, labels.Everything())
			// },
			// GetStatsSummary:       virtualk8s.GetStatsSummary,
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
	 * Create Pod Controller
	 *---------------------------------------------------*/
	DefaultLogger.Info("* Starting the Pod Controller")
	{
		eb := record.NewBroadcaster()
		eb.StartLogging(logrus.Infof)

		pc, err := node.NewPodController(node.PodControllerConfig{
			PodClient:                            k8sclientset.CoreV1(),
			PodInformer:                          podInformer,
			EventRecorder:                        eb.NewRecorder(scheme.Scheme, corev1.EventSource{Component: "hpk-controller"}),
			Provider:                             virtualk8s,
			ConfigMapInformer:                    configMapInformer,
			SecretInformer:                       secretInformer,
			ServiceInformer:                      serviceInformer,
			SyncPodsFromKubernetesRateLimiter:    rateLimiter(),
			DeletePodsFromKubernetesRateLimiter:  rateLimiter(),
			SyncPodStatusFromProviderRateLimiter: rateLimiter(),
		})
		if err != nil {
			return err
		}

		// Start the Pod controller.
		go func() {
			if err := pc.Run(ctx, c.PodSyncWorkers); err != nil && !errors.Is(err, context.Canceled) {
				DefaultLogger.Error(err, "Pod Controller Has failed")
				// handle error
			}
		}()

		// If there is a startup timeout, it does two things:
		// 1. It causes the VK to shut down if we haven't gotten into an operational state in a time period
		// 2. It prevents node advertisement from happening until we're in an operational state
		if c.StartupTimeout > 0 {
			pcCtx, pcCancel := context.WithTimeout(ctx, c.StartupTimeout)
			select {
			case <-pcCtx.Done():
				DefaultLogger.Info("... Aborted before initialization is complete ....")

				pcCancel()
				return pcCtx.Err()
			case <-pc.Ready():
			}
			pcCancel()
			if err := pc.Err(); err != nil {
				return err
			}
		}

		DefaultLogger.Info(" ... Done ...")
	}

	/*---------------------------------------------------
	 * Create Node Controller
	 *---------------------------------------------------*/
	DefaultLogger.Info("* Starting the Node Controller")

	np := node.NewNaiveNodeProvider()
	{
		var taint *corev1.Taint
		if !c.DisableTaint {
			taint, err = getTaint(c)
			if err != nil {
				return err
			}
		}

		virtualNode := virtualk8s.CreateVirtualNode(ctx, c.NodeName, taint)

		ncOpts := []node.NodeControllerOpt{
			node.WithNodeEnableLeaseV1(k8sclientset.CoordinationV1().Leases(corev1.NamespaceNodeLease), 0),
			node.WithNodeStatusUpdateErrorHandler(func(ctx context.Context, err error) error {
				if !k8serrors.IsNotFound(err) {
					return err
				}
				DefaultLogger.Info("node not found")
				newNode := virtualNode.DeepCopy()
				newNode.ResourceVersion = ""
				_, err = k8sclientset.CoreV1().Nodes().Create(ctx, newNode, metav1.CreateOptions{})
				if err != nil {
					return err
				}
				DefaultLogger.Info("registered node")
				return nil
			}),
		}

		nc, err := node.NewNodeController(
			np,
			virtualNode,
			k8sclientset.CoreV1().Nodes(),
			ncOpts...,
		)
		if err != nil {
			return err
		}

		// Start the Node controller.
		go func() {
			if err := nc.Run(ctx); err != nil && err != context.Canceled {
				DefaultLogger.Error(err, "NodeController has failed")

				// handle error
			}
		}()

		// Wait for node controller to become ready
		<-nc.Ready()

		// If we got here, set Node condition Ready.
		setNodeReady(virtualNode)
		if err := np.UpdateStatus(ctx, virtualNode); err != nil {
			return errors.Wrap(err, "error marking the node as ready")
		}

		DefaultLogger.Info("... HPK initialized....")

		// wait for as long the app is running
		<-nc.Done()

		DefaultLogger.Info("... HPK has been gracefully terminated ....")
	}

	return nil
}

func rateLimiter() workqueue.RateLimiter {
	return workqueue.NewMaxOfRateLimiter(
		workqueue.NewItemExponentialFailureRateLimiter(5*time.Millisecond, 10*time.Second),
		// 100 qps, 1000 bucket size.  This is only for retry speed and its only the overall factor (not per item)
		&workqueue.BucketRateLimiter{Limiter: rate.NewLimiter(rate.Limit(100), 1000)},
	)
}

func setNodeReady(n *corev1.Node) {
	for i, c := range n.Status.Conditions {
		if c.Type != "Ready" {
			continue
		}

		c.Message = "systemk is ready"
		c.Reason = "KubeletReady"
		c.Status = corev1.ConditionTrue
		c.LastHeartbeatTime = metav1.Now()
		c.LastTransitionTime = metav1.Now()
		n.Status.Conditions[i] = c
		return
	}
}
