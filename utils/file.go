package utils

import (
	"io/ioutil"

	api "github.com/projectcalico/libcalico-go/lib/apis/v3"
	yaml "github.com/projectcalico/go-yaml-wrapper"
)

// ReadFile reads license from file and returns the LicenseKey resource.
func ReadFile(path string) api.LicenseKey {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		panic(err)
	}

	lic := api.NewLicenseKey()
	err = yaml.Unmarshal(data, &lic)
	if err != nil {
		panic(err)
	}

	return *lic
}

