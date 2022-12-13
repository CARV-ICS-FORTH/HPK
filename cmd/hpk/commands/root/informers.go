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

	"github.com/carv-ics-forth/hpk/pkg/resourcemanager"
	"github.com/pkg/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	kubeinformers "k8s.io/client-go/informers"
	corev1 "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/kubernetes"
)

func AddInformers(ctx context.Context, c Opts, k8sclientset *kubernetes.Clientset) (
	corev1.PodInformer,
	corev1.SecretInformer,
	corev1.ConfigMapInformer,
	corev1.ServiceInformer,
	error,
) {
	DefaultLogger.Info("* Starting Kubernetes Informers ...")

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
		return nil, nil, nil, nil, errors.Wrap(err, "could not create resource manager")
	}

	// Finally, start the informers.
	podInformerFactory.Start(ctx.Done())
	podInformerFactory.WaitForCacheSync(ctx.Done())

	informerFactory.Start(ctx.Done())
	informerFactory.WaitForCacheSync(ctx.Done())

	DefaultLogger.Info("Informers are ready",
		"namespace", c.KubeNamespace,
		"crds", []string{
			"pods", "secrets", "configMap", "service", "serviceAccount",
		})

	return podInformer, secretInformer, configMapInformer, serviceInformer, nil
}
