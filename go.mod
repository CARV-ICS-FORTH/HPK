module github.com/CARV-ICS-FORTH/knoc

go 1.12

require (
	contrib.go.opencensus.io/exporter/ocagent v0.5.0
	github.com/akamensky/argparse v1.3.1
	github.com/containerd/containerd v1.0.2
	github.com/davidmz/go-pageant v1.0.1 // indirect
	github.com/docker/spdystream v0.0.0-20181023171402-6480d4af844c // indirect
	github.com/golang/groupcache v0.0.0-20190129154638-5b532d6fd5ef // indirect
	github.com/google/go-cmp v0.5.2 // indirect
	github.com/pkg/sftp v1.13.0
	github.com/sfreiberg/simplessh v0.0.0-20180301191542-495cbb862a9c
	github.com/sirupsen/logrus v1.4.2
	github.com/virtual-kubelet/node-cli v0.1.2
	github.com/virtual-kubelet/virtual-kubelet v1.2.0
	go.opencensus.io v0.22.0
	golang.org/x/net v0.0.0-20210805182204-aaa1db679c0d // indirect
	golang.org/x/sync v0.0.0-20210220032951-036812b2e83c // indirect
	golang.org/x/sys v0.0.0-20210927052749-1cf2251ac284 // indirect
	golang.org/x/xerrors v0.0.0-20200804184101-5ec99f83aff1 // indirect
	google.golang.org/grpc v1.23.1 // indirect
	gotest.tools v2.2.0+incompatible
	k8s.io/api v0.0.0
	k8s.io/apimachinery v0.0.0
	k8s.io/client-go v11.0.0+incompatible
	k8s.io/klog v0.3.3 // indirect
	k8s.io/kubernetes v1.15.2
	k8s.io/utils v0.0.0-20190607212802-c55fbcfc754a
)

replace k8s.io/legacy-cloud-providers => k8s.io/legacy-cloud-providers v0.0.0-20190805144654-3d5bf3a310c1

replace k8s.io/cloud-provider => k8s.io/cloud-provider v0.0.0-20190805144409-8484242760e7

replace k8s.io/cli-runtime => k8s.io/cli-runtime v0.0.0-20190805143448-a07e59fb081d

replace k8s.io/apiserver => k8s.io/apiserver v0.0.0-20190805142138-368b2058237c

replace k8s.io/csi-translation-lib => k8s.io/csi-translation-lib v0.0.0-20190805144531-3985229e1802

replace k8s.io/cri-api => k8s.io/cri-api v0.0.0-20190531030430-6117653b35f1

replace k8s.io/kube-aggregator => k8s.io/kube-aggregator v0.0.0-20190805142416-fd821fbbb94e

replace k8s.io/kubelet => k8s.io/kubelet v0.0.0-20190805143852-517ff267f8d1

replace k8s.io/kube-controller-manager => k8s.io/kube-controller-manager v0.0.0-20190805144128-269742da31dd

replace k8s.io/apimachinery => k8s.io/apimachinery v0.0.0-20190612205821-1799e75a0719

replace k8s.io/api => k8s.io/api v0.0.0-20190805141119-fdd30b57c827

replace k8s.io/cluster-bootstrap => k8s.io/cluster-bootstrap v0.0.0-20190805144246-c01ee70854a1

replace k8s.io/kube-proxy => k8s.io/kube-proxy v0.0.0-20190805143734-7f1675b90353

replace k8s.io/component-base => k8s.io/component-base v0.0.0-20190805141645-3a5e5ac800ae

replace k8s.io/kube-scheduler => k8s.io/kube-scheduler v0.0.0-20190805144012-2a1ed1f3d8a4

replace k8s.io/apiextensions-apiserver => k8s.io/apiextensions-apiserver v0.0.0-20190805143126-cdb999c96590

replace k8s.io/metrics => k8s.io/metrics v0.0.0-20190805143318-16b07057415d

replace k8s.io/sample-apiserver => k8s.io/sample-apiserver v0.0.0-20190805142637-3b65bc4bb24f

replace k8s.io/code-generator => k8s.io/code-generator v0.0.0-20190612205613-18da4a14b22b

replace k8s.io/client-go => k8s.io/client-go v0.0.0-20190805141520-2fe0317bcee0
