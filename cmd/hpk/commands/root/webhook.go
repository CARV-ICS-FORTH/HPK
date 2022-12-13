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
	"context"
	"fmt"
	"net/http"

	"github.com/carv-ics-forth/hpk/provider"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	kwhhttp "github.com/slok/kubewebhook/v2/pkg/http"
	kwhlogrus "github.com/slok/kubewebhook/v2/pkg/log/logrus"
	kwhmutating "github.com/slok/kubewebhook/v2/pkg/webhook/mutating"
	"github.com/virtual-kubelet/virtual-kubelet/node/api"
)

func AddWebhook(c Opts, virtualk8s *provider.VirtualK8S) {
	DefaultLogger.Info("* Initializing HTTP(s) server ...")

	logrusLogEntry := logrus.NewEntry(logrus.New())
	logrusLogEntry.Logger.SetLevel(logrus.DebugLevel)
	logger := kwhlogrus.NewLogrus(logrusLogEntry)

	mux := http.NewServeMux()

	/*---------------------------------------------------
	 * Mutate CRDs before they arrive to Virtual-Kubelet
	 *---------------------------------------------------*/
	// Create webhook.
	wh, err := kwhmutating.NewWebhook(kwhmutating.WebhookConfig{
		ID:      "pod-annotate",
		Mutator: kwhmutating.MutatorFunc(provider.MutatePod),
		Logger:  logger,
	})
	if err != nil {
		panic(fmt.Errorf("error creating webhook: %w", err))
	}

	// Get HTTP handler from webhook.
	whHandler, err := kwhhttp.HandlerFor(kwhhttp.HandlerConfig{Webhook: wh, Logger: logger})
	if err != nil {
		panic(fmt.Errorf("error creating webhook handler: %w", err))
	}

	mux.Handle("/mutates/pods", whHandler)

	/*---------------------------------------------------
	 * Add handlers for Logs and Statistics
	 *---------------------------------------------------*/
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

	/*---------------------------------------------------
	 * Start the Webhook on the background
	 *---------------------------------------------------*/
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

	DefaultLogger.Info("HTTP(s) server is ready",
		"address", advertisedAddr,
		"cert", c.K8sAPICertFilepath,
		"key", c.K8sAPIKeyFilepath,
	)
}
