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
	"os/user"
	"time"

	"github.com/carv-ics-forth/hpk/cmd/hpk/commands"
	"github.com/carv-ics-forth/hpk/compute"
	"github.com/hashicorp/go-multierror"
	"github.com/sirupsen/logrus"
	vklog "github.com/virtual-kubelet/virtual-kubelet/log"
	"github.com/virtual-kubelet/virtual-kubelet/log/klogv2"
	"golang.org/x/time/rate"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/carv-ics-forth/hpk/provider"
	"github.com/dimiro1/banner"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"github.com/virtual-kubelet/virtual-kubelet/node"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

func logo() string {
	buf := bytes.NewBuffer(nil)

	banner.InitString(buf, true, true, `
{{ .AnsiColor.BrightGreen }}
{{ .Title "HPK" "" 4 }}
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
	/*--
	Don't ask .... Just believe that this is the only way to dump logs from the virtual kubelet to the terminal
	*/
	vklog.L = klogv2.New(vklog.Fields{})

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	/*---------------------------------------------------
	 * Discover System information
	 *---------------------------------------------------*/
	userInfo, err := user.Current()
	if err != nil {
		return errors.Wrapf(err, "unable to get user information")
	}

	compute.Environment.SystemD.User = userInfo.Uid

	/*---------------------------------------------------
	 * Setup a client to Kubernetes API
	 *---------------------------------------------------*/
	restConfig, err := config.GetConfig()
	if err != nil {
		return errors.Wrapf(err, "unable to get kubeconfig")
	}

	{
		k8sclientset, err := kubernetes.NewForConfig(restConfig)
		if err != nil {
			return errors.Wrapf(err, "unable to start kubernetes k8sclientset")
		}

		k8sclient, err := client.New(restConfig, client.Options{})
		if err != nil {
			return errors.Wrapf(err, "unable to start kubernetes client")
		}

		compute.K8SClient = k8sclient
		compute.K8SClientset = k8sclientset
		compute.Environment.ContainerRegistry = c.ContainerRegistry
		compute.Environment.ApptainerBin = c.ApptainerBin

		kubemaster, err := url.Parse(restConfig.Host)
		if err != nil {
			return errors.Wrapf(err, "failed to extract hostname from url '%s'", restConfig.Host)
		}
		compute.Environment.KubeMasterHost = kubemaster.Hostname()

		DefaultLogger.Info("KubeClient is ready",
			"Address", restConfig.Host,
			"ContainerRegistry", compute.Environment.ContainerRegistry,
		)
	}

	/*---------------------------------------------------
	 * Discover Kubernetes DNS server
	 *---------------------------------------------------*/
	{
		dnsEndpoint, err := compute.K8SClientset.CoreV1().Endpoints("kube-system").Get(ctx, "kube-dns", metav1.GetOptions{})
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

		DefaultLogger.Info("KubeDNS client is ready",
			"address", compute.Environment.KubeDNS,
		)
	}

	/*---------------------------------------------------
	 * Register the Provisioner of Virtual Nodes
	 *---------------------------------------------------*/
	virtualk8s, err := provider.NewVirtualK8S(provider.InitConfig{
		InternalIP:        c.KubeletAddress,
		DaemonPort:        c.KubeletPort,
		BuildVersion:      commands.BuildVersion,
		FSPollingInterval: c.FSPollingInterval,
		RestConfig:        restConfig,
	})
	if err != nil {
		return err
	}

	AddAdmissionWebhooks(c, virtualk8s)

	DefaultLogger.Info("Virtual Node Provisioner is ready",
		"Address", virtualk8s.InternalIP,
		"DaemonPort", virtualk8s.DaemonPort,
	)

	/*---------------------------------------------------
	 * Create Informers for CRDs and Pod Controller
	 *---------------------------------------------------*/
	{
		podInformer, secretInformer, configMapInformer, serviceInformer, _, err := AddInformers(ctx, c, compute.K8SClientset)
		if err != nil {
			return errors.Wrapf(err, "failed to add informers")
		}

		DefaultLogger.Info("Informers are ready",
			"namespace", c.KubeNamespace,
			"crds", []string{
				"pods", "secrets", "configMap", "service", "serviceAccount",
			})

		eb := record.NewBroadcaster()
		eb.StartLogging(logrus.Infof)

		pc, err := node.NewPodController(node.PodControllerConfig{
			PodClient:                            compute.K8SClientset.CoreV1(),
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

		DefaultLogger.Info("Pod Controller is Ready")
	}

	/*---------------------------------------------------
	 * Create Node Controller
	 *---------------------------------------------------*/
	{
		np := node.NewNaiveNodeProvider()

		var taint *corev1.Taint
		if !c.DisableTaint {
			taint, err = getTaint(c)
			if err != nil {
				return err
			}
		}

		virtualNode := virtualk8s.NewVirtualNode(ctx, c.NodeName, taint)

		nc, err := node.NewNodeController(
			np,
			virtualNode,
			compute.K8SClientset.CoreV1().Nodes(),
			node.WithNodeEnableLeaseV1(compute.K8SClientset.CoordinationV1().Leases(corev1.NamespaceNodeLease), 0),
			node.WithNodeStatusUpdateErrorHandler(func(ctx context.Context, err error) error {
				if !k8serrors.IsNotFound(err) {
					return err
				}

				DefaultLogger.Info("node not found")
				newNode := virtualNode.DeepCopy()
				newNode.ResourceVersion = ""

				if _, err = compute.K8SClientset.CoreV1().Nodes().Create(ctx, newNode, metav1.CreateOptions{}); err != nil {
					return err
				}

				DefaultLogger.Info("created new node")
				return nil
			}),
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

		DefaultLogger.Info("Node Controller is Ready")

		DefaultLogger.Info("... HPK is successfully initialized and waiting for jobs....")

		// wait for as long the app is running
		<-nc.Done()

		DefaultLogger.Info("... HPK has been gracefully terminated ....")
	}

	return nil
}

func rateLimiter() workqueue.RateLimiter {
	return workqueue.NewMaxOfRateLimiter(
		workqueue.NewItemExponentialFailureRateLimiter(5*time.Millisecond, 10*time.Second),
		// 100 qps, 1000 bucket size.  This is only for retry speed, and it's only the overall factor (not per item)
		&workqueue.BucketRateLimiter{Limiter: rate.NewLimiter(rate.Limit(100), 1000)},
	)
}

func setNodeReady(n *corev1.Node) {
	for i, c := range n.Status.Conditions {
		if c.Type != corev1.NodeReady {
			continue
		}

		c.LastHeartbeatTime = metav1.Now()
		c.LastTransitionTime = metav1.Now()
		c.Reason = "KubeletReady"
		c.Message = "HPK is successfully connected to Slurm"
		c.Status = corev1.ConditionTrue
		n.Status.Conditions[i] = c
		return
	}
}
