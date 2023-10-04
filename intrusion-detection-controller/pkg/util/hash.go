// Copyright 2021-2022 Tigera Inc. All rights reserved.

package util

import (
	"crypto/sha256"
	"fmt"
)

func ComputeSha256Hash(obj interface{}) string {
	encoder := sha256.New()
	if _, err := encoder.Write([]byte(fmt.Sprintf("%v", obj))); err != nil {
		return ""
	}
	return fmt.Sprintf("%x", encoder.Sum(nil))
}
