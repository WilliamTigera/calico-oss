// Copyright (c) 2020-2021 Tigera, Inc. All rights reserved.

package capture_test

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/gomega"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	. "github.com/onsi/ginkgo"
	"github.com/stretchr/testify/mock"

	"github.com/projectcalico/felix/capture"
	"github.com/projectcalico/felix/proto"
	api "github.com/projectcalico/libcalico-go/lib/apis/v3"
	v3 "github.com/projectcalico/libcalico-go/lib/apis/v3"
	"github.com/projectcalico/libcalico-go/lib/options"
	"github.com/projectcalico/libcalico-go/lib/watch"
)

var _ = Describe("PacketCapture Capture Status Writer Tests", func() {
	const hostname = "node1"
	const anotherHostname = "node2"
	const captureDir = "/tmp"
	const name = "capture"
	const anotherName = "anotherCapture"
	const namespace = "ns"

	var packetCaptureNoStatus = v3.PacketCapture{
		TypeMeta: v1.TypeMeta{
			Kind:       "",
			APIVersion: "",
		},
		ObjectMeta: v1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Status: v3.PacketCaptureStatus{},
	}
	var anotherPacketCaptureNoStatus = v3.PacketCapture{
		TypeMeta: v1.TypeMeta{
			Kind:       "",
			APIVersion: "",
		},
		ObjectMeta: v1.ObjectMeta{
			Name:      anotherName,
			Namespace: namespace,
		},
		Status: v3.PacketCaptureStatus{},
	}
	var packetCaptureWithStatus = v3.PacketCapture{
		TypeMeta: v1.TypeMeta{
			Kind:       "",
			APIVersion: "",
		},
		ObjectMeta: v1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Status: v3.PacketCaptureStatus{
			Files: []v3.PacketCaptureFile{
				{
					Node:      hostname,
					Directory: captureDir,
					FileNames: []string{"a", "b", "c"},
				},
			},
		},
	}
	var updatedPacketCapture = v3.PacketCapture{
		TypeMeta: v1.TypeMeta{
			Kind:       "",
			APIVersion: "",
		},
		ObjectMeta: v1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Status: v3.PacketCaptureStatus{
			Files: []v3.PacketCaptureFile{
				{
					Node:      hostname,
					Directory: captureDir,
					FileNames: []string{"a", "b", "c"},
				},
			},
		},
	}
	var anotherUpdatedPacketCapture = v3.PacketCapture{
		TypeMeta: v1.TypeMeta{
			Kind:       "",
			APIVersion: "",
		},
		ObjectMeta: v1.ObjectMeta{
			Name:      anotherName,
			Namespace: namespace,
		},
		Status: v3.PacketCaptureStatus{
			Files: []v3.PacketCaptureFile{
				{
					Node:      hostname,
					Directory: captureDir,
					FileNames: []string{"a", "b", "c"},
				},
			},
		},
	}
	var overrideUpdatedPacketCapture = v3.PacketCapture{
		TypeMeta: v1.TypeMeta{
			Kind:       "",
			APIVersion: "",
		},
		ObjectMeta: v1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Status: v3.PacketCaptureStatus{
			Files: []v3.PacketCaptureFile{
				{
					Node:      hostname,
					Directory: captureDir,
					FileNames: []string{"a", "b", "c", "d"},
				},
			},
		},
	}
	var otherNodesPacketCapture = v3.PacketCapture{
		TypeMeta: v1.TypeMeta{
			Kind:       "",
			APIVersion: "",
		},
		ObjectMeta: v1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Status: v3.PacketCaptureStatus{
			Files: []v3.PacketCaptureFile{
				{
					Node:      anotherHostname,
					Directory: captureDir,
					FileNames: []string{"a", "b", "c"},
				},
			},
		},
	}
	var otherNodesUpdatedPacketCapture = v3.PacketCapture{
		TypeMeta: v1.TypeMeta{
			Kind:       "",
			APIVersion: "",
		},
		ObjectMeta: v1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Status: v3.PacketCaptureStatus{
			Files: []v3.PacketCaptureFile{
				{
					Node:      anotherHostname,
					Directory: captureDir,
					FileNames: []string{"a", "b", "c"},
				},
				{
					Node:      hostname,
					Directory: captureDir,
					FileNames: []string{"a", "b", "c"},
				},
			},
		},
	}
	var updatedPacketCaptureNoFiles = v3.PacketCapture{
		TypeMeta: v1.TypeMeta{
			Kind:       "",
			APIVersion: "",
		},
		ObjectMeta: v1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Status: v3.PacketCaptureStatus{
			Files: []v3.PacketCaptureFile{
				{
					Node:      hostname,
					Directory: captureDir,
				},
			},
		},
	}

	var statusUpdate = &proto.PacketCaptureStatusUpdate{
		Id: &proto.PacketCaptureID{
			Namespace: namespace,
			Name:      name,
		},
		CaptureFiles: []string{"a", "b", "c"},
	}

	var anotherStatusUpdate = &proto.PacketCaptureStatusUpdate{
		Id: &proto.PacketCaptureID{
			Namespace: namespace,
			Name:      anotherName,
		},
		CaptureFiles: []string{"a", "b", "c"},
	}

	var overrideStatusUpdate = &proto.PacketCaptureStatusUpdate{
		Id: &proto.PacketCaptureID{
			Namespace: namespace,
			Name:      name,
		},
		CaptureFiles: []string{"a", "b", "c", "d"},
	}

	var statusUpdateNoFiles = &proto.PacketCaptureStatusUpdate{
		Id: &proto.PacketCaptureID{
			Namespace: namespace,
			Name:      name,
		},
	}

	It("Updates the status of the packet capture", func(done Done) {
		defer close(done)

		var calicoClient = new(mockedCalicoClient)
		var updatesFromDataPlane = make(chan *proto.PacketCaptureStatusUpdate)
		var packetCapture = packetCaptureNoStatus.DeepCopy()

		// Mock CalicoClient to expect one Get and one Update request
		calicoClient.mock.On("Get", mock.Anything, namespace, name,
			options.GetOptions{}).Return(packetCapture, nil).Once()
		calicoClient.mock.On("Update", mock.Anything, &updatedPacketCapture,
			options.SetOptions{}).Return(&updatedPacketCapture, nil).Once()

		// Start StatusWriter
		var statusWriter = capture.NewStatusWriter(hostname, captureDir, calicoClient, updatesFromDataPlane, 1*time.Millisecond)
		go statusWriter.Start()
		defer statusWriter.Stop()

		// Send an update from data plane
		updatesFromDataPlane <- statusUpdate

		// Expect 2 calls to be invoked: one for get and one for update
		Eventually(func() []string {
			var methods []string
			for _, call := range calicoClient.mock.Calls {
				methods = append(methods, call.Method)
			}
			return methods
		}).Should(ConsistOf([]string{"Get", "Update"}))
	})

	It("Retries to updates the status of the packet capture when failing to get capture", func(done Done) {
		defer close(done)

		var calicoClient = new(mockedCalicoClient)
		var updatesFromDataPlane = make(chan *proto.PacketCaptureStatusUpdate)
		var packetCapture = packetCaptureNoStatus.DeepCopy()

		// Mock CalicoClient to expect one Get that returns an error and the rest succeed
		calicoClient.mock.On("Get", mock.Anything, namespace, name,
			options.GetOptions{}).Return(packetCapture, fmt.Errorf("failed to read")).Once()
		calicoClient.mock.On("Get", mock.Anything, namespace, name,
			options.GetOptions{}).Return(packetCapture, nil)
		// Mock CalicoClient to expect Update to succeed
		calicoClient.mock.On("Update", mock.Anything, &updatedPacketCapture,
			options.SetOptions{}).Return(&updatedPacketCapture, nil)

		// Start StatusWriter
		var statusWriter = capture.NewStatusWriter(hostname, captureDir, calicoClient, updatesFromDataPlane, 1*time.Millisecond)
		go statusWriter.Start()
		defer statusWriter.Stop()

		// Send an update from data plane
		updatesFromDataPlane <- statusUpdate

		// Expect 3 calls to be invoked: 2 for get and one for update
		Eventually(func() []string {
			var methods []string
			for _, call := range calicoClient.mock.Calls {
				methods = append(methods, call.Method)
			}
			return methods
		}).Should(ConsistOf([]string{"Get", "Get", "Update"}))
	})

	It("Retries to updates the status of the packet capture when failing to update capture", func(done Done) {
		defer close(done)

		var calicoClient = new(mockedCalicoClient)
		var updatesFromDataPlane = make(chan *proto.PacketCaptureStatusUpdate)
		var packetCapture = packetCaptureNoStatus.DeepCopy()

		// Mock CalicoClient to expect one Get that succeeds
		calicoClient.mock.On("Get", mock.Anything, namespace, name,
			options.GetOptions{}).Return(packetCapture, nil)
		// Mock CalicoClient to expect one Update to return an error and the rest to succeed
		calicoClient.mock.On("Update", mock.Anything, &updatedPacketCapture,
			options.SetOptions{}).Return(&updatedPacketCapture, fmt.Errorf("failed to update")).Once()
		calicoClient.mock.On("Update", mock.Anything, &updatedPacketCapture,
			options.SetOptions{}).Return(&updatedPacketCapture, nil)

		// Start StatusWriter
		var statusWriter = capture.NewStatusWriter(hostname, captureDir, calicoClient, updatesFromDataPlane, 1*time.Millisecond)
		go statusWriter.Start()
		defer statusWriter.Stop()

		// Send an update from data plane
		updatesFromDataPlane <- statusUpdate

		// Expect 4 calls to be invoked: 2 for get and 2 for update
		Eventually(func() []string {
			var methods []string
			for _, call := range calicoClient.mock.Calls {
				methods = append(methods, call.Method)
			}
			return methods
		}).Should(ConsistOf([]string{"Get", "Update", "Get", "Update"}))
	})

	It("Overrides the status of the packet capture", func(done Done) {
		defer close(done)

		var calicoClient = new(mockedCalicoClient)
		var updatesFromDataPlane = make(chan *proto.PacketCaptureStatusUpdate)
		var packetCapture = updatedPacketCapture.DeepCopy()

		// Mock CalicoClient to expect Get to return a status with filesNames : a, b, c
		calicoClient.mock.On("Get", mock.Anything, namespace, name,
			options.GetOptions{}).Return(packetCapture, nil).Once()
		// Mock CalicoClient to expect Update to receive a status with filesNames : a, b, c, d
		calicoClient.mock.On("Update", mock.Anything, &overrideUpdatedPacketCapture,
			options.SetOptions{}).Return(&overrideUpdatedPacketCapture, nil).Once()

		// Start StatusWriter
		var statusWriter = capture.NewStatusWriter(hostname, captureDir, calicoClient, updatesFromDataPlane, 1*time.Millisecond)
		go statusWriter.Start()
		defer statusWriter.Stop()

		// Send an update from data plane
		updatesFromDataPlane <- overrideStatusUpdate

		// Expect 2 calls to be invoked: one for get and one for update
		Eventually(func() []string {
			var methods []string
			for _, call := range calicoClient.mock.Calls {
				methods = append(methods, call.Method)
			}
			return methods
		}).Should(ConsistOf([]string{"Get", "Update"}))
	})

	It("Does not override the status from other hosts", func(done Done) {
		defer close(done)

		var calicoClient = new(mockedCalicoClient)
		var updatesFromDataPlane = make(chan *proto.PacketCaptureStatusUpdate)
		var packetCapture = otherNodesPacketCapture.DeepCopy()

		// Mock CalicoClient to expect Get to return a status with filesNames : a, b, c
		calicoClient.mock.On("Get", mock.Anything, namespace, name,
			options.GetOptions{}).Return(packetCapture, nil).Once()
		// Mock CalicoClient to expect Update to receive a status with filesNames : a, b, c, d
		calicoClient.mock.On("Update", mock.Anything, &otherNodesUpdatedPacketCapture,
			options.SetOptions{}).Return(&otherNodesUpdatedPacketCapture, nil).Once()

		// Start StatusWriter
		var statusWriter = capture.NewStatusWriter(hostname, captureDir, calicoClient, updatesFromDataPlane, 1*time.Millisecond)
		go statusWriter.Start()
		defer statusWriter.Stop()

		// Send an update from data plane
		updatesFromDataPlane <- statusUpdate

		// Expect 2 calls to be invoked: one for get and one for update
		Eventually(func() []string {
			var methods []string
			for _, call := range calicoClient.mock.Calls {
				methods = append(methods, call.Method)
			}
			return methods
		}).Should(ConsistOf([]string{"Get", "Update"}))
	})

	It("Continues to retry when receiving updates", func(done Done) {
		defer close(done)

		var calicoClient = new(mockedCalicoClient)
		var updatesFromDataPlane = make(chan *proto.PacketCaptureStatusUpdate)

		// Mock CalicoClient to expect Get to succeeds
		calicoClient.mock.On("Get", mock.Anything, namespace, name,
			options.GetOptions{}).Return(packetCaptureNoStatus.DeepCopy(), nil)
		calicoClient.mock.On("Get", mock.Anything, namespace, anotherName,
			options.GetOptions{}).Return(anotherPacketCaptureNoStatus.DeepCopy(), nil)
		// Mock CalicoClient to expect Update to return an error and then succeed for PacketCapture "capture"
		calicoClient.mock.On("Update", mock.Anything, &updatedPacketCapture,
			options.SetOptions{}).Return(&updatedPacketCapture, fmt.Errorf("failed to update")).Once()
		calicoClient.mock.On("Update", mock.Anything, &updatedPacketCapture,
			options.SetOptions{}).Return(&updatedPacketCapture, nil)
		// Mock CalicoClient to expect Update to succeed for PacketCapture "anotherCapture"
		calicoClient.mock.On("Update", mock.Anything, &anotherUpdatedPacketCapture,
			options.SetOptions{}).Return(&anotherUpdatedPacketCapture, nil)

		// Start StatusWriter
		var statusWriter = capture.NewStatusWriter(hostname, captureDir, calicoClient, updatesFromDataPlane, 1*time.Millisecond)
		go statusWriter.Start()
		defer statusWriter.Stop()

		// Send updates from data plane
		updatesFromDataPlane <- statusUpdate
		updatesFromDataPlane <- anotherStatusUpdate

		// Expect 6 calls to be invoked: 3 for get and 3 for update
		Eventually(func() []string {
			var methods []string
			for _, call := range calicoClient.mock.Calls {
				methods = append(methods, call.Method)
			}
			return methods
		}).Should(ConsistOf([]string{"Get", "Update", "Get", "Update", "Get", "Update"}))
	})

	It("Updates the status of the packet capture with no files", func(done Done) {
		defer close(done)

		var calicoClient = new(mockedCalicoClient)
		var updatesFromDataPlane = make(chan *proto.PacketCaptureStatusUpdate)
		var packetCapture = packetCaptureWithStatus.DeepCopy()

		// Mock CalicoClient to expect one Get and one Update request
		calicoClient.mock.On("Get", mock.Anything, namespace, name,
			options.GetOptions{}).Return(packetCapture, nil).Once()
		calicoClient.mock.On("Update", mock.Anything, &updatedPacketCaptureNoFiles,
			options.SetOptions{}).Return(&updatedPacketCaptureNoFiles, nil).Once()

		// Start StatusWriter
		var statusWriter = capture.NewStatusWriter(hostname, captureDir, calicoClient, updatesFromDataPlane, 1*time.Millisecond)
		go statusWriter.Start()
		defer statusWriter.Stop()

		// Send an update from data plane
		updatesFromDataPlane <- statusUpdateNoFiles

		// Expect 2 calls to be invoked: one for get and one for update
		Eventually(func() []string {
			var methods []string
			for _, call := range calicoClient.mock.Calls {
				methods = append(methods, call.Method)
			}
			return methods
		}).Should(ConsistOf([]string{"Get", "Update"}))
	})

})

type mockedCalicoClient struct {
	mock mock.Mock
}

func (m *mockedCalicoClient) Create(ctx context.Context, res *api.PacketCapture, opts options.SetOptions) (*api.PacketCapture, error) {
	panic("implement me")
}

func (m *mockedCalicoClient) Update(ctx context.Context, res *api.PacketCapture, opts options.SetOptions) (*api.PacketCapture, error) {
	args := m.mock.Called(ctx, res, opts)
	return args.Get(0).(*api.PacketCapture), args.Error(1)
}

func (m *mockedCalicoClient) Delete(ctx context.Context, namespace, name string, opts options.DeleteOptions) (*api.PacketCapture, error) {
	panic("implement me")
}

func (m *mockedCalicoClient) Get(ctx context.Context, namespace, name string, opts options.GetOptions) (*api.PacketCapture, error) {
	args := m.mock.Called(ctx, namespace, name, opts)
	return args.Get(0).(*api.PacketCapture), args.Error(1)
}

func (m *mockedCalicoClient) List(ctx context.Context, opts options.ListOptions) (*api.PacketCaptureList, error) {
	panic("implement me")
}

func (m *mockedCalicoClient) Watch(ctx context.Context, opts options.ListOptions) (watch.Interface, error) {
	panic("implement me")
}
