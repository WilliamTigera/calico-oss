// Copyright 2019 Tigera Inc. All rights reserved.

package util

import (
	"fmt"
	"net"
)

func Sptr(s string) *string {
	sCopy := s
	return &sCopy
}

type StringPtrWrapper struct {
	S *string
}

func (n StringPtrWrapper) String() string {
	if n.S == nil {
		return "-"
	}
	return *n.S
}

func I64ptr(i int64) *int64 {
	iCopy := i
	return &iCopy
}

type Int64PtrWrapper struct {
	I *int64
}

func (n Int64PtrWrapper) String() string {
	if n.I == nil {
		return "-"
	}
	return fmt.Sprintf("%d", *n.I)
}

func BoolPtr(b bool) *bool {
	bCopy := b
	return &bCopy
}

func UintPtr(i uint) *uint {
	iCopy := i
	return &iCopy
}

func StrPtr(ip *net.IP) *string {
	if ip == nil {
		return nil
	}

	val := ip.String()
	return &val
}

func IPPtr(s string) *net.IP {
	val := net.ParseIP(s)
	return &val
}
