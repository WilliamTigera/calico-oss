module github.com/projectcalico/kube-controllers

go 1.16

require (
	github.com/apparentlymart/go-cidr v1.0.1

	// Elastic repo for ECK v1.0.1
	github.com/elastic/cloud-on-k8s v0.0.0-20200204083752-bcb7468838a8
	github.com/elastic/go-elasticsearch/v7 v7.3.0
	github.com/joho/godotenv v1.3.0
	github.com/kelseyhightower/envconfig v1.4.0
	github.com/onsi/ginkgo v1.15.0
	github.com/onsi/gomega v1.10.5
	github.com/patrickmn/go-cache v2.1.0+incompatible
	github.com/projectcalico/felix v0.0.0-20210806101121-265c9d1f6397
	github.com/projectcalico/libcalico-go v1.7.2
	github.com/projectcalico/typha v0.7.3-0.20210428181500-9e435d5fd964
	github.com/prometheus/client_golang v1.11.0
	github.com/satori/go.uuid v1.2.0
	github.com/sirupsen/logrus v1.8.1
	github.com/smartystreets/assertions v1.0.0 // indirect
	github.com/spf13/pflag v1.0.5
	github.com/stretchr/testify v1.6.1
	github.com/tigera/api v0.0.0-20211118004237-fc020b8219e8
	github.com/tigera/licensing v1.0.1-0.20211118214011-ddaf067af9ab
	go.etcd.io/etcd v0.5.0-alpha.5.0.20201125193152-8a03d2e9614b
	golang.org/x/crypto v0.0.0-20210921155107-089bfa567519
	honnef.co/go/tools v0.0.1-2020.1.4 // indirect
	k8s.io/api v0.22.0
	k8s.io/apimachinery v0.22.0
	k8s.io/apiserver v0.22.0
	k8s.io/client-go v0.22.0
	k8s.io/klog v1.0.0
	k8s.io/kubernetes v1.21.1-rc.0 // indirect
	sigs.k8s.io/controller-runtime v0.9.0-alpha.1 // indirect
)

replace (
	github.com/projectcalico/cni-plugin => github.com/tigera/cni-plugin-private v1.11.1-0.20211119191343-4fbb9aced5ef
	github.com/projectcalico/felix => github.com/tigera/felix-private v0.0.0-20211116222427-a854a08cd0bb
	github.com/projectcalico/libcalico-go => github.com/tigera/libcalico-go-private v1.7.2-0.20211118213557-aaab08536697
	github.com/projectcalico/typha => github.com/tigera/typha-private v0.6.0-beta1.0.20211118215330-647c0d4cbcf1
	github.com/sirupsen/logrus => github.com/projectcalico/logrus v1.0.4-calico

	k8s.io/api => k8s.io/api v0.21.0
	k8s.io/apiextensions-apiserver => k8s.io/apiextensions-apiserver v0.21.0
	k8s.io/apimachinery => k8s.io/apimachinery v0.21.0
	k8s.io/apiserver => k8s.io/apiserver v0.21.0
	k8s.io/cli-runtime => k8s.io/cli-runtime v0.21.0
	k8s.io/client-go => k8s.io/client-go v0.21.0
	k8s.io/cloud-provider => k8s.io/cloud-provider v0.21.0
	k8s.io/cluster-bootstrap => k8s.io/cluster-bootstrap v0.21.0
	k8s.io/code-generator => k8s.io/code-generator v0.21.0
	k8s.io/component-base => k8s.io/component-base v0.21.0
	k8s.io/component-helpers => k8s.io/component-helpers v0.21.0
	k8s.io/controller-manager => k8s.io/controller-manager v0.21.0
	k8s.io/cri-api => k8s.io/cri-api v0.21.0
	k8s.io/csi-translation-lib => k8s.io/csi-translation-lib v0.21.0
	k8s.io/kube-aggregator => k8s.io/kube-aggregator v0.21.0
	k8s.io/kube-controller-manager => k8s.io/kube-controller-manager v0.21.0
	k8s.io/kube-openapi => k8s.io/kube-openapi v0.0.0-20210305001622-591a79e4bda7
	k8s.io/kube-proxy => k8s.io/kube-proxy v0.21.0
	k8s.io/kube-scheduler => k8s.io/kube-scheduler v0.21.0
	k8s.io/kubectl => k8s.io/kubectl v0.21.0
	k8s.io/kubelet => k8s.io/kubelet v0.21.0
	k8s.io/legacy-cloud-providers => k8s.io/legacy-cloud-providers v0.21.0
	k8s.io/metrics => k8s.io/metrics v0.21.0
	k8s.io/mount-utils => k8s.io/mount-utils v0.21.0
	k8s.io/sample-apiserver => k8s.io/sample-apiserver v0.21.0
)
