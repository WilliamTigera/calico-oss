module github.com/projectcalico/typha

go 1.12

require (
	github.com/Workiva/go-datastructures v1.0.50
	github.com/docopt/docopt-go v0.0.0-20160216232012-784ddc588536
	github.com/go-ini/ini v0.0.0-20190327024845-3be5ad479f69
	github.com/kardianos/osext v0.0.0-20190222173326-2bc1f35cddc0
	github.com/mipearson/rfw v0.0.0-20170619235010-6f0a6f3266ba
	github.com/onsi/ginkgo v1.10.1
	github.com/onsi/gomega v1.7.0
	github.com/projectcalico/libcalico-go v0.0.0-00010101000000-000000000000
	github.com/prometheus/client_golang v1.0.0
	github.com/sirupsen/logrus v1.4.2
	github.com/smartystreets/goconvey v0.0.0-20190710185942-9d28bd7c0945 // indirect
	google.golang.org/genproto v0.0.0-20191206224255-0243a4be9c8f // indirect

	gopkg.in/ini.v1 v1.44.0 // indirect

	// k8s.io/apimachinery 1.16.3 is at 72ed19daf4bb
	k8s.io/apimachinery v0.0.0-20191028221656-72ed19daf4bb

	// k8s.io/client-go 1.16.3 is at 6c5935290e33
	k8s.io/client-go v0.0.0-20191114101535-6c5935290e33
)

replace (
	github.com/projectcalico/libcalico-go => github.com/tigera/libcalico-go-private v0.0.0-20200123005450-6c8457330ec4
	github.com/sirupsen/logrus => github.com/projectcalico/logrus v1.0.4-calico
)
