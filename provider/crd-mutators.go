package provider

import (
	"context"

	kwhmodel "github.com/slok/kubewebhook/v2/pkg/model"
	kwhmutating "github.com/slok/kubewebhook/v2/pkg/webhook/mutating"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func MutatePVC(ctx context.Context, review *kwhmodel.AdmissionReview, obj metav1.Object) (*kwhmutating.MutatorResult, error) {
	// we are only interested in newly created PVCs.
	if review.Operation != kwhmodel.OperationCreate {
		return &kwhmutating.MutatorResult{}, nil
	}

	pvc, ok := obj.(*corev1.PersistentVolumeClaim)
	if !ok {
		return &kwhmutating.MutatorResult{}, nil
	}

	// Mutate our object with the required annotations.
	if pvc.Annotations == nil {
		pvc.Annotations = make(map[string]string)
	}
	pvc.Annotations["volume.kubernetes.io/selected-node"] = "hpk-kubelet"

	return &kwhmutating.MutatorResult{MutatedObject: pvc}, nil
}

// MutatePod is used to modify Pods requests before they arrive to the Virtual Kubelet Framework.
// This is because the frameworks drops pods whose containers involve "valueFrom = .status.podIP" semantics.
// Ref: https://github.com/Azure/AKS/issues/2427#issuecomment-1010354262
func MutatePod(ctx context.Context, review *kwhmodel.AdmissionReview, obj metav1.Object) (*kwhmutating.MutatorResult, error) {
	// we are only interested in newly created Pods.
	if review.Operation != kwhmodel.OperationCreate {
		return &kwhmutating.MutatorResult{}, nil
	}

	pod, ok := obj.(*corev1.Pod)
	if !ok {
		return &kwhmutating.MutatorResult{}, nil
	}

	filterOut := corev1.ObjectFieldSelector{
		APIVersion: "v1",
		FieldPath:  "status.podIP",
	}

	for i, container := range pod.Spec.Containers {
		for j, env := range container.Env {
			if env.ValueFrom != nil && env.ValueFrom.FieldRef != nil && *env.ValueFrom.FieldRef == filterOut {
				pod.Spec.Containers[i].Env[j].ValueFrom = nil
				pod.Spec.Containers[i].Env[j].Value = ".status.podIP"
			}
		}
	}

	// Mutate our object with the required annotations.
	if pod.Annotations == nil {
		pod.Annotations = make(map[string]string)
	}
	pod.Annotations["mutated"] = "true"
	pod.Annotations["mutator"] = "pod-annotate"

	return &kwhmutating.MutatorResult{MutatedObject: pod}, nil
}
