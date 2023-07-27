package image_test

import (
	"testing"

	"github.com/carv-ics-forth/hpk/compute/image"
)

func Test_ParseImageName(t *testing.T) {
	tests := []struct {
		name  string
		image string
		want  string
	}{
		{
			name:  "tagWithDigest",
			image: "registry.k8s.io/ingress-nginx/kube-webhook-certgen:v20230407@sha256:543c40fd093964bc9ab509d3e791f9989963021f1e9e4c9c7b6700b02bfb227b",
			want:  "/kube-webhook-certgen_v20230407.sif",
		},
		{
			name:  "StrangeTag",
			image: "docker.io/istio/examples-bookinfo-details-v1:1.16.2",
			want:  "/examples-bookinfo-details-v1_1.16.2.sif",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := image.ParseImageName(tt.image); got != tt.want {
				t.Errorf("parseImageName() = %v, want %v", got, tt.want)
			}
		})
	}
}
