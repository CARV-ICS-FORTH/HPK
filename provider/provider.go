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
	"github.com/carv-ics-forth/knoc/hpc"
	"io"
	"io/fs"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/validation"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/carv-ics-forth/knoc/api"
	"github.com/carv-ics-forth/knoc/pkg/manager"
	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	vkapi "github.com/virtual-kubelet/virtual-kubelet/node/api"
	"github.com/virtual-kubelet/virtual-kubelet/node/api/statsv1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

// var _ vkprovider.InitFunc = (*NewProvider)(nil)

// Provider implements the virtual-kubelet provider interface and stores pods in memory.
type Provider struct {
	InitConfig

	pods map[client.ObjectKey]*corev1.Pod
	// startTime time.Time
	// notifier  func(*corev1.Pod)

	Logger logr.Logger

	resources corev1.ResourceList
}

// InitConfig is the config passed to initialize a registered provider.
type InitConfig struct {
	ConfigPath string
	NodeName   string
	InternalIP string
	DaemonPort int32

	HPC             *hpc.HPCEnvironment
	ResourceManager *manager.ResourceManager
}

// NewProvider creates a new Provider, which implements the PodNotifier interface
func NewProvider(config InitConfig) (*Provider, error) {

	return &Provider{
		InitConfig: config,
		pods:       make(map[client.ObjectKey]*corev1.Pod),
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
		"obj", client.ObjectKeyFromObject(pod),
	)

	defer p.Logger.Info("<- CreatePod",
		"obj", client.ObjectKeyFromObject(pod),
	)

	if err := PrepareExecutionEnvironment(p, pod); err != nil {
		return errors.Wrapf(err, "Couldn not prepare pod's environment ")
	}

	wrappedPod := NewKPod(p, pod, api.DefaultContainerRegistry)

	if err := wrappedPod.ExecuteOperation(ctx, api.SUBMIT); err != nil {
		return errors.Wrapf(err, "failed to submit job")
	}

	key := client.ObjectKeyFromObject(pod)
	p.pods[key] = pod
	// p.notifier(pod)

	return nil

}

// UpdatePod accepts a Pod definition and updates its reference.
func (p *Provider) UpdatePod(_ context.Context, pod *corev1.Pod) error {
	p.Logger.Info("-> UpdatePod",
		"obj", client.ObjectKeyFromObject(pod),
	)

	defer p.Logger.Info("<- UpdatePod",
		"obj", client.ObjectKeyFromObject(pod),
	)

	key := client.ObjectKeyFromObject(pod)

	p.pods[key] = pod
	// p.notifier(pod)

	return nil
}

// DeletePod deletes the specified pod out of memory.
func (p *Provider) DeletePod(ctx context.Context, pod *corev1.Pod) error {
	p.Logger.Info("-> DeletePod",
		"obj", client.ObjectKeyFromObject(pod),
	)

	defer p.Logger.Info("<- DeletePod",
		"obj", client.ObjectKeyFromObject(pod),
	)

	key := client.ObjectKeyFromObject(pod)

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
func (p *Provider) GetPod(ctx context.Context, namespace, name string) (*corev1.Pod, error) {
	p.Logger.Info("-> GetPod",
		"namespace", namespace,
		"name", name,
	)
	defer p.Logger.Info("<- GetPod",
		"namespace", namespace,
		"name", name,
	)

	pods, err := p.GetPods(ctx)
	if err != nil {
		return nil, err
	}
	for _, pod := range pods {
		if pod.Name == name && pod.Namespace == namespace {
			return pod, nil
		}
	}

	return nil, nil
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

	key := client.ObjectKey{
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

func PrepareExecutionEnvironment(p *Provider, pod *corev1.Pod) error {
	/*
		add kubeconfig on remote:$HOME
	*/
	//out, err := exec.Command("test -f .kube/config").Output()
	//if _, ok := err.(*exec.ExitError); !ok {
	//	log.GetLogger(ctx).Debug("Kubeconfig doesn't exist, so we will generate it...")
	//
	//	out, err = exec.Command("/bin/sh", "/home/user0/scripts/prepare_kubeconfig.sh").Output()
	//	if err != nil {
	//		return errors.Wrapf(err, "Could not run kubeconfig_setup script!")
	//	}
	//
	//	log.GetLogger(ctx).Debug("Kubeconfig generated")
	//
	//	if _, err := exec.Command("mkdir -p .kube").Output(); err != nil {
	//		return errors.Wrapf(err, "cannot create dir")
	//	}
	//
	//	if _, err := exec.Command("echo \"" + string(out) + "\" > .kube/config").Output(); err != nil {
	//		return errors.Wrapf(err, "Could not setup kubeconfig on the remote system ")
	//	}
	//
	//	log.GetLogger(ctx).Debug("Kubeconfig installed")
	//}

	/*
		.knoc is used for runtime files
	*/
	if err := os.MkdirAll(api.RuntimeDir, api.SecretPodData); err != nil {
		return errors.Wrapf(err, "cannot create .knoc")
	}

	/*
		Copy volumes from local Pod to remote Environment
	*/
	for _, vol := range pod.Spec.Volumes {
		switch {
		case vol.VolumeSource.ConfigMap != nil:
			//panic("not yet implemented")

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

			// .knoc/podName/volName/*
			podConfigMapDir := filepath.Join(api.RuntimeDir, pod.GetName(), cmvs.Name)

			if err := os.MkdirAll(podConfigMapDir, 0766); err != nil {
				return errors.Wrapf(err, "cannot create '%s'", podConfigMapDir)
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

			// .knoc/podName/secretName/*
			podSecretDir := filepath.Join(api.RuntimeDir, pod.GetName(), svs.SecretName)

			if err := os.MkdirAll(podSecretDir, 0766); err != nil {
				return errors.Wrapf(err, "cannot create dir '%s' for secrets", podSecretDir)
			}

			for k, v := range secret.Data {
				fullPath := filepath.Join(podSecretDir, k)

				if err := os.WriteFile(fullPath, v, fs.FileMode(*svs.DefaultMode)); err != nil {
					return errors.Wrapf(err, "Could not write secret file %s", fullPath)
				}
			}

		case vol.VolumeSource.EmptyDir != nil:
			// .knoc/podName/*
			edPath := filepath.Join(api.RuntimeDir, vol.Name)

			// mounted for every container
			if err := os.MkdirAll(edPath, 0766); err != nil {
				return errors.Wrapf(err, "cannot create emptyDir '%s'", edPath)
			}
			// without size limit for now

		case vol.VolumeSource.DownwardAPI != nil:
			podDownwardApiDir := filepath.Join(api.RuntimeDir, pod.GetName(), vol.Name)

			for _, v := range vol.DownwardAPI.Items {
				fullPath := filepath.Join(podDownwardApiDir, v.Path)
				value, err := ExtractFieldPathAsString(pod, v.FieldRef.FieldPath)

				if err != nil {
					return err
				}

				if err := os.WriteFile(fullPath, []byte(value), fs.FileMode(*vol.Projected.DefaultMode)); err != nil {
					return errors.Wrapf(err, "cannot write config map file '%s'", fullPath)
				}
			}
		case vol.VolumeSource.Projected != nil:
			// .knoc/podName/*
			projectedVolPath := filepath.Join(api.RuntimeDir, pod.GetName(), vol.Name)
			if err := os.MkdirAll(projectedVolPath, 0766); err != nil {
				return errors.Wrapf(err, "cannot create emptyDir '%s'", projectedVolPath)
			}

			for _, projectedSrc := range vol.Projected.Sources {
				switch {
				case projectedSrc.DownwardAPI != nil:
					for _, v := range projectedSrc.DownwardAPI.Items {
						fullPath := filepath.Join(projectedVolPath, v.Path)
						value, err := ExtractFieldPathAsString(pod, v.FieldRef.FieldPath)

						if err != nil {
							return err
						}

						if err := os.WriteFile(fullPath, []byte(value), fs.FileMode(*vol.Projected.DefaultMode)); err != nil {
							return errors.Wrapf(err, "cannot write config map file '%s'", fullPath)
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

					// .knoc/podName/volName/*
					if err := os.MkdirAll(projectedVolPath, 0766); err != nil {
						return errors.Wrapf(err, "cannot create '%s'", projectedVolPath)
					}
					secret, err := p.ResourceManager.GetSecret(serviceAccount.Secrets[0].Name, pod.GetNamespace())
					if err != nil {
						return err
					}

					// TODO: Update upon exceeded expiration date
					// TODO: Ensure that these files are deleted in failure cases
					fullPath := filepath.Join(projectedVolPath, projectedSrc.ServiceAccountToken.Path)
					if err := os.WriteFile(fullPath, secret.Data[projectedSrc.ServiceAccountToken.Path], fs.FileMode(0766)); err != nil {
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

					// .knoc/podName/volName/*
					if err := os.MkdirAll(projectedVolPath, 0766); err != nil {
						return errors.Wrapf(err, "cannot create '%s'", projectedVolPath)
					}

					for k, v := range configMap.Data {
						// TODO: Ensure that these files are deleted in failure cases
						fullPath := filepath.Join(projectedVolPath, k)
						if err := os.WriteFile(fullPath, []byte(v), fs.FileMode(0766)); err != nil {
							return errors.Wrapf(err, "cannot write config map file '%s'", fullPath)
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

					// .knoc/podName/volName/*
					if err := os.MkdirAll(projectedVolPath, 0766); err != nil {
						return errors.Wrapf(err, "cannot create '%s'", projectedVolPath)
					}

					for k, v := range secret.Data {
						// TODO: Ensure that these files are deleted in failure cases
						fullPath := filepath.Join(projectedVolPath, k)
						if err := os.WriteFile(fullPath, v, fs.FileMode(0766)); err != nil {
							return errors.Wrapf(err, "cannot write config map file '%s'", fullPath)
						}
					}
				}
			}
		case vol.VolumeSource.HostPath != nil:
			hostPathVolPath := filepath.Join(api.RuntimeDir, pod.GetName(), vol.Name)

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
