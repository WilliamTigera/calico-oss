module github.com/projectcalico/typha

go 1.16

require (
	github.com/Workiva/go-datastructures v1.0.50
	github.com/docopt/docopt-go v0.0.0-20180111231733-ee0de3bc6815
	github.com/go-ini/ini v0.0.0-20190327024845-3be5ad479f69
	github.com/go-openapi/jsonpointer v0.19.5 // indirect
	github.com/go-openapi/jsonreference v0.19.5 // indirect
	github.com/go-openapi/swag v0.19.14 // indirect
	github.com/kardianos/osext v0.0.0-20190222173326-2bc1f35cddc0
	github.com/mipearson/rfw v0.0.0-20170619235010-6f0a6f3266ba
	github.com/onsi/ginkgo v1.14.1
	github.com/onsi/gomega v1.10.1
	github.com/projectcalico/libcalico-go v1.7.2-0.20210830152008-fb26cc05dab0
	github.com/prometheus/client_golang v1.11.0
	github.com/sirupsen/logrus v1.8.1
	github.com/tigera/api v0.0.0-20211019171312-2dc19d1d38f8
	k8s.io/api v0.21.0
	k8s.io/apimachinery v0.21.0
	k8s.io/client-go v0.21.0
)

replace (
	github.com/projectcalico/libcalico-go => github.com/tigera/libcalico-go-private v1.7.2-0.20211019171316-efd66fec8e8e
	github.com/sirupsen/logrus => github.com/projectcalico/logrus v1.0.4-calico
	google.golang.org/grpc => google.golang.org/grpc v1.29.1
	k8s.io/kube-openapi => k8s.io/kube-openapi v0.0.0-20210305001622-591a79e4bda7
)
