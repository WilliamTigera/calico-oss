// Copyright (c) 2017,2020 Tigera, Inc. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package ifacemonitor

import (
	log "github.com/sirupsen/logrus"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
	//"syscall"
)

type netlinkReal struct {
}

func (nl *netlinkReal) Subscribe(
	linkUpdates chan netlink.LinkUpdate,
	routeUpdates chan netlink.RouteUpdate,
) error {
	cancel := make(chan struct{})

	if err := netlink.LinkSubscribe(linkUpdates, cancel); err != nil {
		log.WithError(err).Panic("Failed to subscribe to link updates")
		return err
	}
	if err := netlink.RouteSubscribe(routeUpdates, cancel); err != nil {
		log.WithError(err).Panic("Failed to subscribe to addr updates")
		return err
	}

	return nil
}

func (nl *netlinkReal) LinkList() ([]netlink.Link, error) {
	return netlink.LinkList()
}

func (nl *netlinkReal) ListLocalRoutes(link netlink.Link, family int) ([]netlink.Route, error) {
	routeFilter := &netlink.Route{}
	if link != nil {
		routeFilter.LinkIndex = link.Attrs().Index
	}
	routeFilter.Table = unix.RT_TABLE_LOCAL
	return netlink.RouteListFiltered(family, routeFilter, netlink.RT_FILTER_TABLE|netlink.RT_FILTER_OIF)
}
