// Copyright (c) 2016 Tigera, Inc. All rights reserved.
package commands

import (
	"fmt"
)

const VERSION = "0.2.0"

func Version() error {
	fmt.Println("Version:     ", VERSION)
	fmt.Println("Build date:  ", BUILD_DATE)
	fmt.Println("Git tag ref: ", GIT_DESCRIPTION)
	fmt.Println("Git commit:  ", GIT_REVISION)
	return nil
}
