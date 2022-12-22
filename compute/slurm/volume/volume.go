package volume

import (
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/wait"
)

// Attributes represents the attributes of this mounter.
type Attributes struct {
	ReadOnly       bool
	Managed        bool
	SELinuxRelabel bool
}

// MounterArgs provides more easily extensible arguments to Mounter
type MounterArgs struct {
	// When FsUser is set, the ownership of the volume will be modified to be
	// owned and writable by FsUser. Otherwise, there is no side effects.
	// Currently only supported with projected service account tokens.
	FsUser              *int64
	FsGroup             *int64
	FSGroupChangePolicy *corev1.PodFSGroupChangePolicy
	DesiredSize         *resource.Quantity
	SELinuxLabel        string
}

// NotFoundBackoff is the recommended backoff for a resource that is required,
// but is not created yet. For instance, when mounting configmap volumes to pods.
// TODO: in future version, the backoff can be self-modified depending on the load of the controller.
var NotFoundBackoff = wait.Backoff{
	Steps:    4,
	Duration: 2 * time.Second,
	Factor:   5.0,
	Jitter:   0.1,
}
