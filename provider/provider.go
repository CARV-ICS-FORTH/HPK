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
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/carv-ics-forth/knoc/api"
	"github.com/carv-ics-forth/knoc/hpc"
	"github.com/carv-ics-forth/knoc/pkg/manager"
	"github.com/carv-ics-forth/knoc/pkg/utils"
	"github.com/carv-ics-forth/knoc/provider/paths"
	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	vkapi "github.com/virtual-kubelet/virtual-kubelet/node/api"
	"github.com/virtual-kubelet/virtual-kubelet/node/api/statsv1alpha1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

// var _ vkprovider.InitFunc = (*NewProvider)(nil)

// Provider implements the virtual-kubelet provider interface and stores pods in memory.
type Provider struct {
	InitConfig

	Logger logr.Logger
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
		Logger:     zap.New(zap.UseDevMode(true)),
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

	if err := wrappedPod.CreatePod(ctx, pod); err != nil {
		return errors.Wrapf(err, "failed to submit job")
	}

	return wrappedPod.Save()
}

// UpdatePod accepts a Pod definition and updates its reference.
func (p *Provider) UpdatePod(_ context.Context, pod *corev1.Pod) error {
	p.Logger.Info("-> UpdatePod",
		"obj", api.ObjectKeyFromObject(pod),
		"phase", pod.Status.Phase,
	)

	defer p.Logger.Info("<- UpdatePod",
		"obj", api.ObjectKeyFromObject(pod),
		"phase", pod.Status.Phase,
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

	/*
		DeletePod takes a Kubernetes Pod and deletes it from the provider.
		TODO: Once a pod is deleted, the provider is expected to call the NotifyPods callback with a terminal pod status
		where all the containers are in a terminal state, as well as the pod.
		DeletePod may be called multiple times for the same pod
	*/
	if err := DeletePodDirectory(pod); err != nil {
		return errors.Wrapf(err, "failed to delete pods")
	}

	pod.Status.Phase = corev1.PodSucceeded

	for i := range pod.Status.InitContainerStatuses {
		pod.Status.InitContainerStatuses[i].State.Terminated = &corev1.ContainerStateTerminated{
			ExitCode:    0,
			Signal:      0,
			Reason:      "PodIsDeleted",
			Message:     "Pod is being deleted",
			StartedAt:   metav1.Time{Time: time.Now()},
			FinishedAt:  metav1.Time{Time: time.Now()},
			ContainerID: "",
		}
	}

	for i := range pod.Status.ContainerStatuses {
		pod.Status.ContainerStatuses[i].State.Terminated = &corev1.ContainerStateTerminated{
			ExitCode:    0,
			Signal:      0,
			Reason:      "PodIsDeleted",
			Message:     "Pod is being deleted",
			StartedAt:   metav1.Time{Time: time.Now()},
			FinishedAt:  metav1.Time{Time: time.Now()},
			ContainerID: "",
		}
	}

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

	if kpod.Pod == nil {
		panic("undefined behavior. this should never happen.")
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
		panic("this should never happen -- or at least happen gracefully and documented")
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

	/*
		stdout, err := os.OpenFile(paths.StdOutputFilePath(key, containerName))
		if err != nil {
			return nil, errors.Wrapf(err, "Failed to open stdout")
		}

		stderr, err := os.OpenFile(paths.StdErrorFilePath(key, containerName))
		if err != nil {
			return nil, errors.Wrapf(err, "Failed to open stdout")
		}

		io.MultiReader()

		key := api.ObjectKey{
			Namespace: namespace,
			Name:      podName,
		}


	*/
	_ = key
	/*
		// if _, exists := p.pods[key]; !exists {
			return nil, errors.Errorf("pod '%s' is not known to the provider", key)
		}

		// search in pod running directory, for the container.out
		containerLogsFile := filepath.Join(api.RuntimeDir, podName, containerName, ".out")

		f, err := os.Open(containerLogsFile)
		if err != nil {
			return nil, errors.Wrapf(err, "cannot open file '%s'", containerLogsFile)
		}

		return f, nil

	*/
	return nil, errors.Errorf("ContainerLogs is not yet supported")
}

// RunInContainer executes a command in a container in the pod, copying data
// between in/out/err and the container's stdin/stdout/stderr.
func (p *Provider) RunInContainer(_ context.Context, namespace, name, container string, cmd []string, attach vkapi.AttachIO) error {
	p.Logger.Info("-> RunInContainer")
	defer p.Logger.Info("<- RunInContainer")

	p.Logger.Info("RunInContainer not supported", "cmd", cmd)

	return nil
}

func (p *Provider) ConfigureNode(_ context.Context, _ corev1.Node) {
	p.Logger.Info("-> ConfigureNode")
	defer p.Logger.Info("<- ConfigureNode")
}

func (p *Provider) GetStatsSummary(context.Context) (*statsv1alpha1.Summary, error) {
	p.Logger.Info("-> GetStatsSummary")
	defer p.Logger.Info("<- GetStatsSummary")

	panic("poutsakia")
}

/************************************************************

		Needed to avoid dependencies on nodeutils.

************************************************************/

func CreateBackendVolumes(p *Provider, kpod *KPod) error {
	pod := kpod.Pod

	/*
		Copy volumes from local Pod to remote Environment
	*/
	for _, vol := range pod.Spec.Volumes {
		switch {
		case vol.VolumeSource.ConfigMap != nil:
			/*  == Example ==
			volumes:
			  - name: config-volume
			    configMap:
			      name: game-config
			*/
			source := vol.VolumeSource.ConfigMap

			configMap, err := p.ResourceManager.GetConfigMap(source.Name, pod.GetNamespace())
			if k8serrors.IsNotFound(err) {
				if source.Optional != nil && !*source.Optional {
					return errors.Wrapf(err, "Configmap '%s' is required by Pod '%s' and does not exist", source.Name, pod.GetName())
				}
			}

			if err != nil {
				return errors.Wrapf(err, "Error getting configmap '%s' from API server", pod.Name)
			}

			// .knoc/namespace/podName/volName/*
			podConfigMapDir, err := kpod.CreateSubDirectory(vol.Name)
			if err != nil {
				return errors.Wrapf(err, "cannot create dir '%s' for configMap", podConfigMapDir)
			}

			for k, v := range configMap.Data {
				// TODO: Ensure that these files are deleted in failure cases
				fullPath := filepath.Join(podConfigMapDir, k)

				if err := os.WriteFile(fullPath, []byte(v), fs.FileMode(*source.DefaultMode)); err != nil {
					return errors.Wrapf(err, "cannot write config map file '%s'", fullPath)
				}
			}

		case vol.VolumeSource.Secret != nil:
			source := vol.VolumeSource.Secret

			secret, err := p.ResourceManager.GetSecret(source.SecretName, pod.Namespace)
			if k8serrors.IsNotFound(err) {
				if source.Optional != nil && !*source.Optional {
					return errors.Wrapf(err, "Secret '%s' is required by Pod '%s' and does not exist", source.SecretName, pod.GetName())
				}
			}

			if err != nil {
				return errors.Wrapf(err, "Error getting secret '%s' from API server", pod.Name)
			}

			// .knoc/namespace/podName/secretName/*
			podSecretDir, err := kpod.CreateSubDirectory(vol.Name)
			if err != nil {
				return errors.Wrapf(err, "cannot create dir '%s' for secrets", podSecretDir)
			}

			for k, v := range secret.Data {
				fullPath := filepath.Join(podSecretDir, k)

				if err := os.WriteFile(fullPath, v, fs.FileMode(*source.DefaultMode)); err != nil {
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
				value, err := utils.ExtractFieldPathAsString(pod, item.FieldRef.FieldPath)
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

						value, err := utils.ExtractFieldPathAsString(pod, item.FieldRef.FieldPath)
						if err != nil {
							return err
						}

						if err := os.WriteFile(itemPath, []byte(value), fs.FileMode(*vol.Projected.DefaultMode)); err != nil {
							return errors.Wrapf(err, "cannot write config map file '%s'", itemPath)
						}
					}

				case projectedSrc.ServiceAccountToken != nil:
					serviceAccount, err := p.ResourceManager.GetServiceAccount(pod.Spec.ServiceAccountName, pod.GetNamespace())
					if err != nil {
						return errors.Wrapf(err, "Error getting configmap '%s' from API server", pod.Name)
					}

					if serviceAccount == nil {
						panic("this should never happen ")
					}

					if serviceAccount.AutomountServiceAccountToken != nil && *serviceAccount.AutomountServiceAccountToken {
						for _, secretRef := range serviceAccount.Secrets {
							secret, err := p.ResourceManager.GetSecret(secretRef.Name, pod.GetNamespace())
							if err != nil {
								return errors.Wrapf(err, "get secret error")
							}

							// TODO: Update upon exceeded expiration date
							// TODO: Ensure that these files are deleted in failure cases
							fullPath := filepath.Join(projectedVolPath, projectedSrc.ServiceAccountToken.Path)
							if err := os.WriteFile(fullPath, secret.Data[projectedSrc.ServiceAccountToken.Path], fs.FileMode(0o766)); err != nil {
								return errors.Wrapf(err, "cannot write config map file '%s'", fullPath)
							}
						}
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
			hostPathVolPath := filepath.Join(paths.RuntimeDir(api.ObjectKeyFromObject(kpod.Pod)), vol.Name)

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
