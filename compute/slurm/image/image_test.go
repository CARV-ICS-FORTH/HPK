package image

import (
	"testing"

	"k8s.io/client-go/util/homedir"
)

func Test_imageFilePath(t *testing.T) {

	type args struct {
		image string
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "tagWithDigest",
			args: args{
				image: "registry.k8s.io/ingress-nginx/kube-webhook-certgen:v20230407@sha256:543c40fd093964bc9ab509d3e791f9989963021f1e9e4c9c7b6700b02bfb227b",
			},
			want: homedir.HomeDir() + "/.hpk/.images/kube-webhook-certgen_v20230407.sif",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := imageFilePath(tt.args.image); got != tt.want {
				t.Errorf("imageFilePath() = %v, want %v", got, tt.want)
			}
		})
	}
}
