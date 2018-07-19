package cmd

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"

	"gopkg.in/yaml.v2"

	api "github.com/projectcalico/libcalico-go/lib/apis/v3"
	"github.com/spf13/cobra"
	"github.com/tigera/licensing/client"
)

var (
	lFile string
)

func init() {
	DecodeLicenseCmd.Flags().StringVarP(&lFile, "file", "f", "", "Decode a given license file")
	DecodeLicenseCmd.MarkFlagRequired("file")
}

var DecodeLicenseCmd = &cobra.Command{
	Use:   "decode",
	Short: "Decode licenses",
	Run: func(cmd *cobra.Command, args []string) {
		f, err := ioutil.ReadFile(lFile)
		if err != nil {
			log.Fatalf("error reading license file: %v", err)
		}

		lic := api.NewLicenseKey()

		err = yaml.Unmarshal(f, lic)
		if err != nil {
			log.Fatal(err)
		}

		cl, err := client.Decode(*lic)
		if err != nil {
			log.Fatal(err)
		}

		bits, err := json.Marshal(cl)
		if err != nil {
			log.Fatal(err)
		}

		fmt.Print(string(bits))
	},
}
