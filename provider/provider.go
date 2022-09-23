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
// limitations under the License.package main

package provider

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/carv-ics-forth/knoc/hpc"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/carv-ics-forth/knoc/api"
	"github.com/carv-ics-forth/knoc/pkg/manager"
	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	vkapi "github.com/virtual-kubelet/virtual-kubelet/node/api"
	"github.com/virtual-kubelet/virtual-kubelet/node/api/statsv1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

// var _ vkprovider.InitFunc = (*NewProvider)(nil)

// Provider implements the virtual-kubelet provider interface and stores pods in memory.
type Provider struct {
	InitConfig

	pods map[api.ObjectKey]*corev1.Pod
	// startTime time.Time
	// notifier  func(*corev1.Pod)

	Logger logr.Logger

	resources corev1.ResourceList
}

// InitConfig is the config passed to initialize a registered provider.
type InitConfig struct {
	ConfigPath      string
	NodeName        string
	InternalIP      string
	DaemonPort      int32
	HPC             *hpc.HPCEnvironment
	ResourceManager *manager.ResourceManager
}

// NewProvider creates a new Provider, which implements the PodNotifier interface
func NewProvider(config InitConfig) (*Provider, error) {
	return &Provider{
		InitConfig: config,
		pods:       make(map[api.ObjectKey]*corev1.Pod),
		// startTime:  time.Now(),
		// notifier:   nil,
		Logger: zap.New(zap.UseDevMode(true)),
		resources: corev1.ResourceList{
			"cpu":    resource.MustParse("30"),
			"memory": resource.MustParse("10Gi"),
			"pods":   resource.MustParse("100"),
		},
	}, nil
}

/************************************************************

		Implements node.PodLifecycleHandler

************************************************************/

// CreatePod accepts a Pod definition and stores it in memory.
func (p *Provider) CreatePod(ctx context.Context, pod *corev1.Pod) error {
	p.Logger.Info("-> CreatePod",
		"obj", api.ObjectKeyFromObject(pod),
	)

	defer p.Logger.Info("<- CreatePod",
		"obj", api.ObjectKeyFromObject(pod),
	)

	wrappedPod, err := NewKPod(p, pod, api.DefaultContainerRegistry)
	if err != nil {
		return errors.Wrapf(err, "failed to create kpod")
	}

	if err := wrappedPod.ExecuteOperation(ctx, api.SUBMIT); err != nil {
		return errors.Wrapf(err, "failed to submit job")
	}

	return wrappedPod.Save()
}

// UpdatePod accepts a Pod definition and updates its reference.
func (p *Provider) UpdatePod(_ context.Context, pod *corev1.Pod) error {
	p.Logger.Info("-> UpdatePod",
		"obj", api.ObjectKeyFromObject(pod),
	)

	defer p.Logger.Info("<- UpdatePod",
		"obj", api.ObjectKeyFromObject(pod),
	)

	kpod, err := NewKPod(p, pod, api.DefaultContainerRegistry)
	if err != nil {
		return errors.Wrap(err, "Could not create Pod from the underlying filesystem")
	}

	if err := kpod.Save(); err != nil {
		return errors.Wrap(err, "Could not save Pod to the underlying filesystem")
	}

	return nil
}

// DeletePod deletes the specified pod out of memory.
func (p *Provider) DeletePod(ctx context.Context, pod *corev1.Pod) error {
	p.Logger.Info("-> DeletePod",
		"obj", api.ObjectKeyFromObject(pod),
	)

	defer p.Logger.Info("<- DeletePod",
		"obj", api.ObjectKeyFromObject(pod),
	)

	key := api.ObjectKeyFromObject(pod)

	// check if the pod is managed
	if _, exists := p.pods[key]; !exists {
		return errors.Errorf("key '%s' not found", key)
	}

	//if err := to_be_removed.RemoteExecution(p, ctx, api.DELETE, pod); err != nil {
	//	return errors.Wrapf(err, "Failed to Delete pod '%s'", pod.GetName())
	//}

	// p.notifier(pod)
	delete(p.pods, key)

	return nil
}

// GetPod returns a pod by name that is stored in memory.
func (p *Provider) GetPod(_ context.Context, namespace, name string) (*corev1.Pod, error) {
	p.Logger.Info("-> GetPod",
		"namespace", namespace,
		"name", name,
	)
	defer p.Logger.Info("<- GetPod",
		"namespace", namespace,
		"name", name,
	)

	key := api.ObjectKey{
		Namespace: namespace,
		Name:      name,
	}

	kpod, err := LoadKPod(p, key, api.DefaultContainerRegistry)
	if err != nil {
		return nil, errors.Wrap(err, "Failed to load KPod")
	}

	return kpod.Pod, nil
}

// GetPodStatus returns the status of a pod by name that is "running".
// returns nil if a pod by that name is not found.
func (p *Provider) GetPodStatus(ctx context.Context, namespace, name string) (*corev1.PodStatus, error) {
	p.Logger.Info("-> GetPodStatus",
		"namespace", namespace,
		"name", name,
	)
	defer p.Logger.Info("<- GetPodStatus",
		"namespace", namespace,
		"name", name,
	)

	pod, err := p.GetPod(ctx, namespace, name)
	if err != nil {
		return nil, errors.Wrapf(err, "cannot get pod")
	}

	if pod == nil {
		return nil, nil
	}

	return &pod.Status, nil
}

// GetPods returns a list of all pods known to be "running".
func (p *Provider) GetPods(_ context.Context) ([]*corev1.Pod, error) {
	p.Logger.Info("-> GetPods")
	defer p.Logger.Info("<- GetPods")

	/*
		TODO: inspect the underlying system for Pods in the Runtime Directory (.knoc)
		Pod status: inspect .knoc (network, volumes, lifetime)
		Container status: query SLURM
		Hint: use batch operations
	*/

	/*

		pods := make([]*corev1.Pod, 0)
		for _, cg := range p.GetCgs() {
			c := cg
			pod, err := containerGroupToPod(&c)
			if err != nil {
				msg := fmt.Sprint("error converting container group to pod", cg.ContainerGroupId, err)
				log.G(context.TODO()).WithField("Method", "GetPods").Info(msg)
				continue
			}
			pods = append(pods, pod)
		}
		return pods, nil

		var pods []*corev1.Pod

		// TODO: we need "running" pods, or everything ?

		for _, pod := range p.pods {
			pods = append(pods, pod)
		}

	*/

	return nil, nil
}

/************************************************************

		Implements vkapi.Provider

************************************************************/

// GetContainerLogs retrieves the logs of a container by name from the provider.
func (p *Provider) GetContainerLogs(_ context.Context, namespace, podName, containerName string, _ vkapi.ContainerLogOpts) (io.ReadCloser, error) {
	p.Logger.Info("-> GetContainerLogs")
	defer p.Logger.Info("<- GetContainerLogs")

	key := api.ObjectKey{
		Namespace: namespace,
		Name:      podName,
	}

	if _, exists := p.pods[key]; !exists {
		return nil, errors.Errorf("pod '%s' is not known to the provider", key)
	}

	// search in pod running directory, for the container.out
	containerLogsFile := filepath.Join(api.RuntimeDir, podName, containerName, ".out")

	f, err := os.Open(containerLogsFile)
	if err != nil {
		return nil, errors.Wrapf(err, "cannot open file '%s'", containerLogsFile)
	}

	return f, nil
}

// RunInContainer executes a command in a container in the pod, copying data
// between in/out/err and the container's stdin/stdout/stderr.
func (p *Provider) RunInContainer(_ context.Context, namespace, name, container string, cmd []string, attach vkapi.AttachIO) error {
	p.Logger.Info("-> RunInContainer")
	defer p.Logger.Info("<- RunInContainer")

	p.Logger.Info("RunInContainer not supported", "cmd", cmd)

	return nil
}

func (p *Provider) ConfigureNode(_ context.Context, n *corev1.Node) {
	p.Logger.Info("-> ConfigureNode")
	defer p.Logger.Info("<- ConfigureNode")

	internalIP := "127.0.0.1"

	n.Status.Capacity = p.resources
	n.Status.Allocatable = p.resources
	n.Status.Conditions = p.nodeConditions()
	n.Status.Addresses = []corev1.NodeAddress{{
		Type:    "InternalIP",
		Address: internalIP,
	}}

	n.Status.DaemonEndpoints = corev1.NodeDaemonEndpoints{
		KubeletEndpoint: corev1.DaemonEndpoint{
			Port: p.InitConfig.DaemonPort,
		},
	}

	n.Status.NodeInfo.OperatingSystem = runtime.GOOS
	n.Status.NodeInfo.Architecture = "amd64"
	n.ObjectMeta.Labels["alpha.service-controller.kubernetes.io/exclude-balancer"] = "true"
	n.ObjectMeta.Labels["node.kubernetes.io/exclude-from-external-load-balancers"] = "true"
}

// NodeConditions returns a list of conditions (Ready, OutOfDisk, etc), for updates to the node status
// within Kubernetes.
func (p *Provider) nodeConditions() []corev1.NodeCondition {
	p.Logger.Info("-> nodeConditions")
	defer p.Logger.Info("<- nodeConditions")

	// TODO: Make this configurable
	return []corev1.NodeCondition{
		{
			Type:               corev1.NodeReady,
			Status:             corev1.ConditionTrue,
			LastHeartbeatTime:  metav1.Now(),
			LastTransitionTime: metav1.Now(),
			Reason:             "KubeletPending",
			Message:            "kubelet is pending.",
		},
		{
			Type:               corev1.NodeMemoryPressure,
			Status:             corev1.ConditionFalse,
			LastHeartbeatTime:  metav1.Now(),
			LastTransitionTime: metav1.Now(),
			Reason:             "KubeletHasSufficientMemory",
			Message:            "kubelet has sufficient memory available",
		},
		{
			Type:               corev1.NodeDiskPressure,
			Status:             corev1.ConditionFalse,
			LastHeartbeatTime:  metav1.Now(),
			LastTransitionTime: metav1.Now(),
			Reason:             "KubeletHasNoDiskPressure",
			Message:            "kubelet has no disk pressure",
		},
		{
			Type:               corev1.NodePIDPressure,
			Status:             corev1.ConditionFalse,
			LastHeartbeatTime:  metav1.Now(),
			LastTransitionTime: metav1.Now(),
			Reason:             "KubeletHasNoPIDPressure",
			Message:            "kubelet has no PID pressure",
		},
		{
			Type:               corev1.NodeNetworkUnavailable,
			Status:             corev1.ConditionFalse,
			LastHeartbeatTime:  metav1.Now(),
			LastTransitionTime: metav1.Now(),
			Reason:             "RouteCreated",
			Message:            "RouteController created a route",
		},
	}
}

func (p *Provider) GetStatsSummary(context.Context) (*statsv1alpha1.Summary, error) {
	p.Logger.Info("-> GetStatsSummary")
	defer p.Logger.Info("<- GetStatsSummary")

	panic("poutsakia")
}

/************************************************************

		Needed to avoid dependencies on nodeutils.

************************************************************/

func (p *Provider) Capacity(ctx context.Context) corev1.ResourceList {
	p.Logger.Info("-> Capacity")
	defer p.Logger.Info("<- Capacity ", "resources", p.resources)

	return p.resources
}

func (p *Provider) NodeConditions(ctx context.Context) []corev1.NodeCondition {
	p.Logger.Info("-> NodeConditions")
	defer p.Logger.Info("<- NodeConditions")

	return p.nodeConditions()
}

func (p *Provider) NodeAddresses(ctx context.Context) []corev1.NodeAddress {
	p.Logger.Info("-> NodeAddresses")
	defer p.Logger.Info("<- NodeAddresses")

	return nil
}

func (p *Provider) NodeDaemonEndpoints(ctx context.Context) *corev1.NodeDaemonEndpoints {
	p.Logger.Info("-> NodeDaemonEndpoints")
	defer p.Logger.Info("<- NodeDaemonEndpoints")

	return &corev1.NodeDaemonEndpoints{}
}

func PrepareExecutionEnvironment(p *Provider, kpod *KPod) error {
	pod := kpod.Pod

	/*
		Copy volumes from local Pod to remote Environment
	*/
	for _, vol := range pod.Spec.Volumes {
		switch {
		case vol.VolumeSource.ConfigMap != nil:

			cmvs := vol.VolumeSource.ConfigMap

			configMap, err := p.ResourceManager.GetConfigMap(cmvs.Name, pod.GetNamespace())
			{ // err check
				if cmvs.Optional != nil && !*cmvs.Optional {
					return errors.Wrapf(err, "Configmap '%s' is required by Pod '%s' and does not exist", cmvs.Name, pod.GetName())
				}

				if err != nil {
					return errors.Wrapf(err, "Error getting configmap '%s' from API server", pod.Name)
				}

				if configMap == nil {
					continue
				}
			}

			// .knoc/namespace/podName/volName/*
			podConfigMapDir, err := kpod.CreateSubDirectory(cmvs.Name)
			if err != nil {
				return errors.Wrapf(err, "cannot create dir '%s' for configMap", podConfigMapDir)
			}

			for k, v := range configMap.Data {
				// TODO: Ensure that these files are deleted in failure cases
				fullPath := filepath.Join(podConfigMapDir, k)

				if err := os.WriteFile(fullPath, []byte(v), fs.FileMode(*cmvs.DefaultMode)); err != nil {
					return errors.Wrapf(err, "cannot write config map file '%s'", fullPath)
				}
			}

		case vol.VolumeSource.Secret != nil:
			svs := vol.VolumeSource.Secret

			secret, err := p.ResourceManager.GetSecret(svs.SecretName, pod.Namespace)
			{
				if svs.Optional != nil && !*svs.Optional {
					return errors.Errorf("Secret %s is required by Pod %s and does not exist", svs.SecretName, pod.Name)
				}
				if err != nil {
					return errors.Wrapf(err, "cannot get secret '%s' from API Server", pod.GetName())
				}

				if secret == nil {
					continue
				}
			}

			// .knoc/namespace/podName/secretName/*
			podSecretDir, err := kpod.CreateSubDirectory(svs.SecretName)
			if err != nil {
				return errors.Wrapf(err, "cannot create dir '%s' for secrets", podSecretDir)
			}

			for k, v := range secret.Data {
				fullPath := filepath.Join(podSecretDir, k)

				if err := os.WriteFile(fullPath, v, fs.FileMode(*svs.DefaultMode)); err != nil {
					return errors.Wrapf(err, "Could not write secret file %s", fullPath)
				}
			}

		case vol.VolumeSource.EmptyDir != nil:
			// .knoc/namespace/podName/*

			// mounted for every container
			emptyDir, err := kpod.CreateSubDirectory(vol.Name)
			if err != nil {
				return errors.Wrapf(err, "cannot create dir '%s' for emptyDir", emptyDir)
			}
			// without size limit for now

		case vol.VolumeSource.DownwardAPI != nil:
			// .knoc/namespace/podName/*
			downApiDir, err := kpod.CreateSubDirectory(vol.Name)
			if err != nil {
				return errors.Wrapf(err, "cannot create dir '%s' for downwardApi", downApiDir)
			}

			for _, item := range vol.DownwardAPI.Items {
				itemPath := filepath.Join(downApiDir, item.Path)
				value, err := ExtractFieldPathAsString(pod, item.FieldRef.FieldPath)
				if err != nil {
					return err
				}

				if err := os.WriteFile(itemPath, []byte(value), fs.FileMode(*vol.Projected.DefaultMode)); err != nil {
					return errors.Wrapf(err, "cannot write config map file '%s'", itemPath)
				}
			}
		case vol.VolumeSource.Projected != nil:
			// .knoc/namespace/podName/*
			projectedVolPath, err := kpod.CreateSubDirectory(vol.Name)
			if err != nil {
				return errors.Wrapf(err, "cannot create dir '%s' for projected volume", projectedVolPath)
			}

			for _, projectedSrc := range vol.Projected.Sources {
				switch {
				case projectedSrc.DownwardAPI != nil:
					for _, item := range projectedSrc.DownwardAPI.Items {
						itemPath := filepath.Join(projectedVolPath, item.Path)

						value, err := ExtractFieldPathAsString(pod, item.FieldRef.FieldPath)
						if err != nil {
							return err
						}

						if err := os.WriteFile(itemPath, []byte(value), fs.FileMode(*vol.Projected.DefaultMode)); err != nil {
							return errors.Wrapf(err, "cannot write config map file '%s'", itemPath)
						}
					}
				case projectedSrc.ServiceAccountToken != nil:
					serviceAccount, err := p.ResourceManager.GetServiceAccount(pod.Spec.ServiceAccountName, pod.GetNamespace())

					{ // err check
						//if projectedSrc.ConfigMap.Optional != nil && !*projectedSrc.ConfigMap.Optional {
						//	return errors.Wrapf(err, "Configmap '%s' is required by Pod '%s' and does not exist", projectedSrc.ConfigMap.Name, pod.GetName())
						//}

						if err != nil {
							return errors.Wrapf(err, "Error getting configmap '%s' from API server", pod.Name)
						}

						if serviceAccount == nil {
							continue
						}
					}

					secret, err := p.ResourceManager.GetSecret(serviceAccount.Secrets[0].Name, pod.GetNamespace())
					if err != nil {
						return err
					}

					// TODO: Update upon exceeded expiration date
					// TODO: Ensure that these files are deleted in failure cases
					fullPath := filepath.Join(projectedVolPath, projectedSrc.ServiceAccountToken.Path)
					if err := os.WriteFile(fullPath, secret.Data[projectedSrc.ServiceAccountToken.Path], fs.FileMode(0o766)); err != nil {
						return errors.Wrapf(err, "cannot write config map file '%s'", fullPath)
					}

				case projectedSrc.ConfigMap != nil:
					configMap, err := p.ResourceManager.GetConfigMap(projectedSrc.ConfigMap.Name, pod.GetNamespace())
					{ // err check
						if projectedSrc.ConfigMap.Optional != nil && !*projectedSrc.ConfigMap.Optional {
							return errors.Wrapf(err, "Configmap '%s' is required by Pod '%s' and does not exist", projectedSrc.ConfigMap.Name, pod.GetName())
						}

						if err != nil {
							return errors.Wrapf(err, "Error getting configmap '%s' from API server", pod.Name)
						}

						if configMap == nil {
							continue
						}
					}

					for k, item := range configMap.Data {
						// TODO: Ensure that these files are deleted in failure cases
						itemPath := filepath.Join(projectedVolPath, k)
						if err := os.WriteFile(itemPath, []byte(item), fs.FileMode(0o766)); err != nil {
							return errors.Wrapf(err, "cannot write config map file '%s'", itemPath)
						}
					}
				case projectedSrc.Secret != nil:
					secret, err := p.ResourceManager.GetSecret(projectedSrc.Secret.Name, pod.GetNamespace())
					{ // err check
						if projectedSrc.Secret.Optional != nil && !*projectedSrc.Secret.Optional {
							return errors.Wrapf(err, "Secret '%s' is required by Pod '%s' and does not exist", projectedSrc.Secret.Name, pod.GetName())
						}

						if err != nil {
							return errors.Wrapf(err, "Error getting secret '%s' from API server", pod.Name)
						}

						if secret == nil {
							continue
						}
					}

					for k, item := range secret.Data {
						// TODO: Ensure that these files are deleted in failure cases
						itemPath := filepath.Join(projectedVolPath, k)
						if err := os.WriteFile(itemPath, item, fs.FileMode(0o766)); err != nil {
							return errors.Wrapf(err, "cannot write config map file '%s'", itemPath)
						}
					}
				}
			}
		case vol.VolumeSource.HostPath != nil:
			hostPathVolPath := filepath.Join(kpod.RuntimeDir(), vol.Name)

			switch *vol.VolumeSource.HostPath.Type {
			case corev1.HostPathUnset:
				// For backwards compatible, leave it empty if unset
				// .knoc/podName/volName/*
				if err := os.MkdirAll(hostPathVolPath, 755); err != nil {
					return errors.Wrapf(err, "cannot create '%s'", hostPathVolPath)
				}
			case corev1.HostPathDirectoryOrCreate:
				// If nothing exists at the given path, an empty directory will be created there
				// as needed with file mode 0755, having the same group and ownership with Kubelet.

				// .knoc/podName/volName/*
				if err := os.MkdirAll(hostPathVolPath, 755); err != nil {
					return errors.Wrapf(err, "cannot create '%s'", hostPathVolPath)
				}
			case corev1.HostPathDirectory:
				// A directory must exist at the given path
				// .knoc/podName/volName/*
				if err := os.MkdirAll(hostPathVolPath, 755); err != nil {
					return errors.Wrapf(err, "cannot create '%s'", hostPathVolPath)
				}

			case corev1.HostPathFileOrCreate:
				// If nothing exists at the given path, an empty file will be created there
				// as needed with file mode 0644, having the same group and ownership with Kubelet.
				// .knoc/podName/volName/*
				f, err := os.Create(hostPathVolPath)
				if err != nil {
					return errors.Wrapf(err, "cannot create '%s'", hostPathVolPath)
				}

				if err := f.Close(); err != nil {
					return errors.Wrapf(err, "cannot close file '%s'", hostPathVolPath)
				}
			case corev1.HostPathFile:
				// A file must exist at the given path
				// .knoc/podName/volName/*
				f, err := os.Create(hostPathVolPath)
				if err != nil {
					return errors.Wrapf(err, "cannot create '%s'", hostPathVolPath)
				}

				if err := f.Close(); err != nil {
					return errors.Wrapf(err, "cannot close file '%s'", hostPathVolPath)
				}
			case corev1.HostPathSocket, corev1.HostPathCharDev, corev1.HostPathBlockDev:
				// A UNIX socket/char device/ block device must exist at the given path
				continue
			default:
				panic("bug")
			}
		}
	}

	return nil
}

// FormatMap formats map[string]string to a string.
func FormatMap(m map[string]string) (fmtStr string) {
	// output with keys in sorted order to provide stable output
	keys := sets.NewString()
	for key := range m {
		keys.Insert(key)
	}
	for _, key := range keys.List() {
		fmtStr += fmt.Sprintf("%v=%q\n", key, m[key])
	}
	fmtStr = strings.TrimSuffix(fmtStr, "\n")

	return
}

// ExtractFieldPathAsString extracts the field from the given object
// and returns it as a string.  The object must be a pointer to an
// API type.
func ExtractFieldPathAsString(obj interface{}, fieldPath string) (string, error) {
	accessor, err := meta.Accessor(obj)
	if err != nil {
		return "", nil
	}

	if path, subscript, ok := SplitMaybeSubscriptedPath(fieldPath); ok {
		switch path {
		case "metadata.annotations":
			if errs := validation.IsQualifiedName(strings.ToLower(subscript)); len(errs) != 0 {
				return "", fmt.Errorf("invalid key subscript in %s: %s", fieldPath, strings.Join(errs, ";"))
			}
			return accessor.GetAnnotations()[subscript], nil
		case "metadata.labels":
			if errs := validation.IsQualifiedName(subscript); len(errs) != 0 {
				return "", fmt.Errorf("invalid key subscript in %s: %s", fieldPath, strings.Join(errs, ";"))
			}
			return accessor.GetLabels()[subscript], nil
		default:
			return "", fmt.Errorf("fieldPath %q does not support subscript", fieldPath)
		}
	}

	switch fieldPath {
	case "metadata.annotations":
		return FormatMap(accessor.GetAnnotations()), nil
	case "metadata.labels":
		return FormatMap(accessor.GetLabels()), nil
	case "metadata.name":
		return accessor.GetName(), nil
	case "metadata.namespace":
		return accessor.GetNamespace(), nil
	case "metadata.uid":
		return string(accessor.GetUID()), nil
	}

	return "", fmt.Errorf("unsupported fieldPath: %v", fieldPath)
}

// SplitMaybeSubscriptedPath checks whether the specified fieldPath is
// subscripted, and
//   - if yes, this function splits the fieldPath into path and subscript, and
//     returns (path, subscript, true).
//   - if no, this function returns (fieldPath, "", false).
//
// Example inputs and outputs:
//
//	"metadata.annotations['myKey']" --> ("metadata.annotations", "myKey", true)
//	"metadata.annotations['a[b]c']" --> ("metadata.annotations", "a[b]c", true)
//	"metadata.labels['']"           --> ("metadata.labels", "", true)
//	"metadata.labels"               --> ("metadata.labels", "", false)
func SplitMaybeSubscriptedPath(fieldPath string) (string, string, bool) {
	if !strings.HasSuffix(fieldPath, "']") {
		return fieldPath, "", false
	}
	s := strings.TrimSuffix(fieldPath, "']")
	parts := strings.SplitN(s, "['", 2)
	if len(parts) < 2 {
		return fieldPath, "", false
	}
	if len(parts[0]) == 0 {
		return fieldPath, "", false
	}
	return parts[0], parts[1], true
}

/*
// NotifyPods is called to set a pod notifier callback function. This should be called before any operations are done
// within the provider.
func (p *Provider) NotifyPods(ctx context.Context, f func(*corev1.Pod)) {
	p.notifier = f
	go p.statusLoop(ctx)
}

func (p *Provider) statusLoop(ctx context.Context) {
	t := time.NewTimer(5 * time.Second)
	if !t.Stop() {
		<-t.C
	}

	for {
		t.Reset(5 * time.Second)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}

		checkPodsStatus(p, ctx)
	}
}

*/
