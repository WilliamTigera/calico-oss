module github.com/tigera/licensing

go 1.13

require (
	github.com/davecgh/go-spew v1.1.1
	github.com/emicklei/go-restful v2.11.1+incompatible // indirect
	github.com/go-openapi/spec v0.19.4 // indirect
	github.com/go-sql-driver/mysql v1.4.1
	github.com/gogo/protobuf v1.3.1 // indirect
	github.com/kelseyhightower/envconfig v1.4.0
	github.com/konsorten/go-windows-terminal-sequences v1.0.2 // indirect
	github.com/onsi/gomega v1.7.1
	github.com/projectcalico/go-yaml-wrapper v0.0.0-20191112210931-090425220c54
	github.com/projectcalico/libcalico-go v1.7.2
	github.com/satori/go.uuid v1.2.0
	github.com/sirupsen/logrus v1.4.2
	github.com/spf13/cobra v0.0.3
	github.com/spf13/pflag v1.0.5
	github.com/stretchr/testify v1.4.0
	google.golang.org/appengine v1.6.5 // indirect
	gopkg.in/square/go-jose.v2 v2.2.3-0.20190111193340-cbf0fd6a984a
	gopkg.in/yaml.v2 v2.2.8
)

replace github.com/projectcalico/libcalico-go => github.com/tigera/libcalico-go-private v0.0.0-20200617214044-dd9aed324aa2
