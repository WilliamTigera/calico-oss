module github.com/projectcalico/app-policy

go 1.12

require (
	github.com/docopt/docopt-go v0.0.0-20160216232012-784ddc588536
	github.com/envoyproxy/data-plane-api v0.0.0-20190513203724-4a93c6d2d917 // indirect
	github.com/gogo/googleapis v1.0.0
	github.com/gogo/protobuf v1.2.2-0.20190723190241-65acae22fc9d
	github.com/golang/protobuf v1.3.2 // indirect
	github.com/konsorten/go-windows-terminal-sequences v1.0.2 // indirect
	github.com/lyft/protoc-gen-validate v0.0.6 // indirect
	github.com/onsi/gomega v1.5.0
	github.com/projectcalico/libcalico-go v0.0.0-00000000000000-000000000000
	github.com/sirupsen/logrus v1.4.2
	golang.org/x/net v0.0.0-20190812203447-cdfb69ac37fc
	google.golang.org/grpc v1.19.0
	// k8s.io/apimachinery 1.16.3 is at 72ed19daf4bb
	k8s.io/apimachinery v0.0.0-20191028221656-72ed19daf4bb

)

replace (
	github.com/projectcalico/libcalico-go => github.com/tigera/libcalico-go-private v0.0.0-20200103165626-2c83fde7c5ce

	github.com/sirupsen/logrus => github.com/projectcalico/logrus v1.0.4-calico
)
