// Copyright (c) 2021 Tigera, Inc. All rights reserved.
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

package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"sync"
	"syscall"

	"github.com/docopt/docopt-go"
	log "github.com/sirupsen/logrus"
)

const usage = `tproxy: acts as a transparent proxy for Felix fv testing.

Usage:
  tproxy <port-svc> <port-np> [--gid=<gid>]`

func main() {
	log.SetLevel(log.InfoLevel)
	args, err := docopt.ParseArgs(usage, nil, "v0.1")
	if err != nil {
		println(usage)
		log.WithError(err).Fatal("Failed to parse usage")
	}

	log.WithField("args", args).Info("Parsed arguments")

	gid := -1
	if args["--gid"] != nil {
		gid, err = strconv.Atoi(args["--gid"].(string))
		if err != nil {
			log.WithError(err).Fatal("gid not a number")
		}
	}

	if gid >= 0 {
		if err := syscall.Setgid(gid); err != nil {
			log.WithError(err).Fatalf("Failed to set gid to %d", gid)
		}
	}

	portSvc, err := strconv.Atoi(args["<port-svc>"].(string))
	if err != nil {
		log.WithError(err).Fatal("port not a number")
	}

	listenerSvc, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(0, 0, 0, 0), Port: portSvc})
	if err != nil {
		log.WithError(err).Fatalf("Failed to listen on port %d", portSvc)
	}

	f, err := listenerSvc.File()
	if err != nil {
		log.WithError(err).Fatal("Failed to get listener fd")
	}

	if err = syscall.SetsockoptInt(int(f.Fd()), syscall.SOL_IP, syscall.IP_TRANSPARENT, 1); err != nil {
		log.WithError(err).Fatal("Failed to set IP_TRANSPARENT on listener")
	}

	log.Infof("Listening on port %d", portSvc)

	f.Close()

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			down, err := listenerSvc.Accept()
			if err != nil {
				log.WithError(err).Errorf("Failed to accept connection")
				continue
			}

			go handleConnection(down, true)
		}
	}()

	portNp, err := strconv.Atoi(args["<port-np>"].(string))
	if err != nil {
		log.WithError(err).Fatal("port not a number")
	}

	listenerNp, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(0, 0, 0, 0), Port: portNp})
	if err != nil {
		log.WithError(err).Fatalf("Failed to listen on port %d", portNp)
	}

	f, err = listenerNp.File()
	if err != nil {
		log.WithError(err).Fatal("Failed to get listener fd")
	}

	if err = syscall.SetsockoptInt(int(f.Fd()), syscall.SOL_IP, syscall.IP_TRANSPARENT, 1); err != nil {
		log.WithError(err).Fatal("Failed to set IP_TRANSPARENT on listener")
	}

	log.Infof("Listening on port %d for node ports", portNp)

	f.Close()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			down, err := listenerNp.Accept()
			if err != nil {
				log.WithError(err).Errorf("Failed to accept connection")
				continue
			}

			go handleConnection(down, false)
		}
	}()

	wg.Wait() // infinitely
}

func getPreDNATDest(c net.Conn) net.Addr {
	tcpConn, ok := c.(*net.TCPConn)
	if !ok {
		log.Fatalf("Connection of type %T", c)
	}

	f, err := tcpConn.File()
	if err != nil {
		log.WithError(err).Fatal("Failed to get listener fd")
	}
	defer f.Close()

	// GetsockoptIPv6Mreq returns more bytes then we want. First 16 as a slice - it is a hack that works ;-)
	// The underlying getsockopt is unfortunately, who knows why, unexported :'(
	addr, err := syscall.GetsockoptIPv6Mreq(int(f.Fd()), syscall.IPPROTO_IP, 80 /* SO_ORIGINAL_DST */)
	if err != nil {
		log.WithError(err).Fatal("Failed to get SO_ORIGINAL_DST")
	}

	var ret net.TCPAddr

	ret.Port = int(binary.BigEndian.Uint16(addr.Multiaddr[2:4]))
	ret.IP = net.IP(addr.Multiaddr[4:8])

	return &ret
}

func handleConnection(down net.Conn, origSrc bool) {
	defer down.Close()

	preDNATDest := getPreDNATDest(down)

	log.Infof("Accepted connection from %s to %s orig dest %s", down.RemoteAddr(), down.LocalAddr(), preDNATDest)
	clientNetAddr := down.RemoteAddr().(*net.TCPAddr)
	clientIP := [4]byte{}
	copy(clientIP[:], clientNetAddr.IP.To4())
	clientAddr := syscall.SockaddrInet4{Addr: clientIP, Port: 0 /* pick a random port */}

	serverNetAddr := down.LocalAddr().(*net.TCPAddr)
	serverIP := [4]byte{}
	copy(serverIP[:], serverNetAddr.IP.To4())
	serverAddr := syscall.SockaddrInet4{Addr: serverIP, Port: serverNetAddr.Port}

	s, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_STREAM, syscall.IPPROTO_TCP)
	if err != nil {
		log.WithError(err).Fatal("Failed to create TCP socket")
	}

	if err = syscall.SetsockoptInt(s, syscall.SOL_IP, syscall.IP_TRANSPARENT, 1); err != nil {
		log.WithError(err).Fatal("Failed to set IP_TRANSPARENT on socket")
	}

	if origSrc {
		if err = syscall.Bind(s, &clientAddr); err != nil {
			log.WithError(err).Infof("Failed to bind socket to %v", clientAddr)
			return
		}
	}

	if err = syscall.Connect(s, &serverAddr); err != nil {
		log.WithError(err).Infof("Failed to connect socket to %v", serverAddr)
		return
	}

	fd := os.NewFile(uintptr(s), fmt.Sprintf("proxy-conn-%s-%s", down.LocalAddr(), down.RemoteAddr()))
	up, err := net.FileConn(fd)
	if err != nil {
		log.WithError(err).Fatalf("Failed to convert socket to connection %v - %v", clientAddr, serverAddr)
	}
	defer up.Close()

	log.Infof("Proxying from %s to %s orig dest %s", down.RemoteAddr(), up.RemoteAddr(), preDNATDest)
	proxyConnection(down, up)
}

func proxyConnection(down, up net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		io.Copy(down, up)
		wg.Done()
	}()

	go func() {
		io.Copy(up, down)
		wg.Done()
	}()

	wg.Wait()
}
