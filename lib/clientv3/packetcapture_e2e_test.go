// Copyright (c) 2020 Tigera, Inc. All rights reserved.

package clientv3_test

import (
	"context"
	"time"

	"github.com/tigera/api/pkg/lib/numorstring"

	log "github.com/sirupsen/logrus"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/ginkgo/extensions/table"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	apiv3 "github.com/tigera/api/pkg/apis/projectcalico/v3"

	"github.com/projectcalico/libcalico-go/lib/apiconfig"
	"github.com/projectcalico/libcalico-go/lib/backend"
	"github.com/projectcalico/libcalico-go/lib/clientv3"
	"github.com/projectcalico/libcalico-go/lib/options"
	"github.com/projectcalico/libcalico-go/lib/testutils"
	"github.com/projectcalico/libcalico-go/lib/watch"
)

var _ = testutils.E2eDatastoreDescribe("PacketCapture tests", testutils.DatastoreAll, func(config apiconfig.CalicoAPIConfig) {

	ctx := context.Background()
	name1 := "capture-1"
	name2 := "capture-2"
	namespace1 := "namespace-1"
	namespace2 := "namespace-2"
	protocol := numorstring.ProtocolFromString("TCP")

	spec1 := apiv3.PacketCaptureSpec{
		Selector: "all()",
		Filters: []apiv3.PacketCaptureRule{
			{
				Ports: []numorstring.Port{
					numorstring.SinglePort(80),
				},
				Protocol: &protocol,
			},
		},
		StartTime: &metav1.Time{Time: time.Unix(0, 0)},
		EndTime:   &metav1.Time{Time: time.Unix(0, 0).Add(time.Hour)},
	}
	spec2 := apiv3.PacketCaptureSpec{
		Selector: "all()",
		Filters: []apiv3.PacketCaptureRule{
			{
				Ports: []numorstring.Port{
					numorstring.SinglePort(80),
				},
			},
			{
				Protocol: &protocol,
			},
		},
		StartTime: &metav1.Time{Time: time.Unix(0, 0)},
		EndTime:   &metav1.Time{Time: time.Unix(0, 0).Add(time.Hour)},
	}

	DescribeTable("PacketCapture e2e CRUD tests",
		func(name1, name2 string, spec1, spec2 apiv3.PacketCaptureSpec) {
			c, err := clientv3.New(config)
			Expect(err).NotTo(HaveOccurred())

			be, err := backend.NewClient(config)
			Expect(err).NotTo(HaveOccurred())
			be.Clean()

			By("Updating the PacketCapture before it is created")
			_, outError := c.PacketCaptures().Update(ctx, &apiv3.PacketCapture{
				ObjectMeta: metav1.ObjectMeta{Namespace: namespace1, Name: name1, ResourceVersion: "1234", CreationTimestamp: metav1.Now(), UID: "test-fail-packetCapture"},
				Spec:       spec1,
			}, options.SetOptions{})
			Expect(outError).To(HaveOccurred())
			Expect(outError.Error()).To(ContainSubstring("resource does not exist: PacketCapture(" + namespace1 + "/" + name1 + ") with error:"))

			By("Attempting to creating a new PacketCapture with namespace1/name1/spec1 and a non-empty ResourceVersion")
			_, outError = c.PacketCaptures().Create(ctx, &apiv3.PacketCapture{
				ObjectMeta: metav1.ObjectMeta{Namespace: namespace1, Name: name1, ResourceVersion: "12345"},
				Spec:       spec1,
			}, options.SetOptions{})

			Expect(outError).To(HaveOccurred())
			Expect(outError.Error()).To(Equal("error with field Metadata.ResourceVersion = '12345' (field must not be set for a Create request)"))

			By("Creating a new PacketCapture with namespace1/name1")
			res1, outError := c.PacketCaptures().Create(ctx, &apiv3.PacketCapture{
				ObjectMeta: metav1.ObjectMeta{Namespace: namespace1, Name: name1},
				Spec:       spec1,
			}, options.SetOptions{})
			Expect(outError).NotTo(HaveOccurred())
			Expect(res1).To(MatchResource(apiv3.KindPacketCapture, namespace1, name1, spec1))

			// Track the version of the original data for name1.
			rv1_1 := res1.ResourceVersion

			By("Attempting to create the same PacketCapture with namespace1/name1, but with spec2")
			_, outError = c.PacketCaptures().Create(ctx, &apiv3.PacketCapture{
				ObjectMeta: metav1.ObjectMeta{Namespace: namespace1, Name: name1},
				Spec:       spec2,
			}, options.SetOptions{})
			Expect(outError).To(HaveOccurred())
			Expect(outError.Error()).To(Equal("resource already exists: PacketCapture(" + namespace1 + "/" + name1 + ")"))

			By("Getting PacketCapture (namespace1/name1) and comparing the output against spec1")
			res, outError := c.PacketCaptures().Get(ctx, namespace1, name1, options.GetOptions{})
			Expect(outError).NotTo(HaveOccurred())
			Expect(res).To(MatchResource(apiv3.KindPacketCapture, namespace1, name1, spec1))
			Expect(res.ResourceVersion).To(Equal(res1.ResourceVersion))

			By("Getting PacketCapture (namespace2/name2) before it is created")
			_, outError = c.PacketCaptures().Get(ctx, namespace2, name2, options.GetOptions{})
			Expect(outError).To(HaveOccurred())
			Expect(outError.Error()).To(ContainSubstring("resource does not exist: PacketCapture(" + namespace2 + "/" + name2 + ") with error:"))

			By("Listing all the PacketCaptures, expecting a single result with namespace1/name1/spec1")
			outList, outError := c.PacketCaptures().List(ctx, options.ListOptions{})
			Expect(outError).NotTo(HaveOccurred())
			Expect(outList.Items).To(ConsistOf(
				testutils.Resource(apiv3.KindPacketCapture, namespace1, name1, spec1),
			))

			By("Creating a new PacketCapture with namespace2/name2/spec2")
			res2, outError := c.PacketCaptures().Create(ctx, &apiv3.PacketCapture{
				ObjectMeta: metav1.ObjectMeta{Namespace: namespace2, Name: name2},
				Spec:       spec2,
			}, options.SetOptions{})
			Expect(outError).NotTo(HaveOccurred())
			Expect(res2).To(MatchResource(apiv3.KindPacketCapture, namespace2, name2, spec2))

			By("Getting PacketCapture (namespace2/name2) and comparing the output against spec2")
			res, outError = c.PacketCaptures().Get(ctx, namespace2, name2, options.GetOptions{})
			Expect(outError).NotTo(HaveOccurred())
			Expect(res2).To(MatchResource(apiv3.KindPacketCapture, namespace2, name2, spec2))
			Expect(res.ResourceVersion).To(Equal(res2.ResourceVersion))

			By("Listing all the PacketCaptures, expecting a two results with namespace1/name1/spec1 and namespace2/name2/spec2")
			outList, outError = c.PacketCaptures().List(ctx, options.ListOptions{})
			Expect(outError).NotTo(HaveOccurred())
			Expect(outList.Items).To(ConsistOf(
				testutils.Resource(apiv3.KindPacketCapture, namespace1, name1, spec1),
				testutils.Resource(apiv3.KindPacketCapture, namespace2, name2, spec2),
			))

			By("Updating PacketCapture namespace1/name1 with spec2")
			res1.Spec = spec2
			res1, outError = c.PacketCaptures().Update(ctx, res1, options.SetOptions{})
			Expect(outError).NotTo(HaveOccurred())
			Expect(res1).To(MatchResource(apiv3.KindPacketCapture, namespace1, name1, spec2))

			By("Attempting to update the PacketCapture without a Creation Timestamp")
			res, outError = c.PacketCaptures().Update(ctx, &apiv3.PacketCapture{
				ObjectMeta: metav1.ObjectMeta{Namespace: namespace1, Name: name1, ResourceVersion: "1234", UID: "test-fail-packetCapture"},
				Spec:       spec1,
			}, options.SetOptions{})
			Expect(outError).To(HaveOccurred())
			Expect(res).To(BeNil())
			Expect(outError.Error()).To(Equal("error with field Metadata.CreationTimestamp = '0001-01-01 00:00:00 +0000 UTC' (field must be set for an Update request)"))

			By("Attempting to update the PacketCapture without a UID")
			res, outError = c.PacketCaptures().Update(ctx, &apiv3.PacketCapture{
				ObjectMeta: metav1.ObjectMeta{Namespace: namespace1, Name: name1, ResourceVersion: "1234", CreationTimestamp: metav1.Now()},
				Spec:       spec1,
			}, options.SetOptions{})
			Expect(outError).To(HaveOccurred())
			Expect(res).To(BeNil())
			Expect(outError.Error()).To(Equal("error with field Metadata.UID = '' (field must be set for an Update request)"))

			// Track the version of the updated name1 data.
			rv1_2 := res1.ResourceVersion

			By("Updating PacketCapture namespace1/name1 without specifying a resource version")
			res1.Spec = spec1
			res1.ObjectMeta.ResourceVersion = ""
			_, outError = c.PacketCaptures().Update(ctx, res1, options.SetOptions{})
			Expect(outError).To(HaveOccurred())
			Expect(outError.Error()).To(Equal("error with field Metadata.ResourceVersion = '' (field must be set for an Update request)"))

			By("Updating PacketCapture name1 using the previous resource version")
			res1.Spec = spec1
			res1.ResourceVersion = rv1_1

			_, outError = c.PacketCaptures().Update(ctx, res1, options.SetOptions{})
			Expect(outError).To(HaveOccurred())
			Expect(outError.Error()).To(Equal("update conflict: PacketCapture(" + namespace1 + "/" + name1 + ")"))

			if config.Spec.DatastoreType != apiconfig.Kubernetes {
				By("Getting PacketCapture (namespace1/name1) with the original resource version and comparing the output against spec1")
				res, outError = c.PacketCaptures().Get(ctx, namespace1, name1, options.GetOptions{ResourceVersion: rv1_1})
				Expect(outError).NotTo(HaveOccurred())
				Expect(res).To(MatchResource(apiv3.KindPacketCapture, namespace1, name1, spec1))
				Expect(res.ResourceVersion).To(Equal(rv1_1))
			}

			By("Getting PacketCapture (namespace1/name1) with the updated resource version and comparing the output against spec2")
			res, outError = c.PacketCaptures().Get(ctx, namespace1, name1, options.GetOptions{ResourceVersion: rv1_2})
			Expect(outError).NotTo(HaveOccurred())
			Expect(res).To(MatchResource(apiv3.KindPacketCapture, namespace1, name1, spec2))
			Expect(res.ResourceVersion).To(Equal(rv1_2))

			if config.Spec.DatastoreType != apiconfig.Kubernetes {
				By("Listing PacketCaptures with the original resource version and checking for a single result with name1/spec1")
				outList, outError = c.PacketCaptures().List(ctx, options.ListOptions{ResourceVersion: rv1_1})
				Expect(outError).NotTo(HaveOccurred())
				Expect(outList.Items).To(ConsistOf(
					testutils.Resource(apiv3.KindPacketCapture, namespace1, name1, spec1),
				))
			}

			By("Listing PacketCaptures with the latest resource version and checking for two results with name1/spec2 and name2/spec2")
			outList, outError = c.PacketCaptures().List(ctx, options.ListOptions{})
			Expect(outError).NotTo(HaveOccurred())
			Expect(outList.Items).To(ConsistOf(
				testutils.Resource(apiv3.KindPacketCapture, namespace1, name1, spec2),
				testutils.Resource(apiv3.KindPacketCapture, namespace2, name2, spec2),
			))

			By("Setting status1 on resource")
			var capturingState = apiv3.PacketCaptureStateCapturing
			status1 := apiv3.PacketCaptureStatus{
				Files: []apiv3.PacketCaptureFile{
					{
						Node:      "node1",
						FileNames: []string{"file1", "file2"},
						State:     &capturingState,
					},
				},
			}
			res.Status = status1
			log.Infof("Before update %#v", res)
			res, outError = c.PacketCaptures().Update(ctx, res, options.SetOptions{})
			Expect(outError).ToNot(HaveOccurred())
			log.Infof("After update %#v", res)
			Expect(res.Status.Files).To(HaveLen(1))
			Expect(res).To(MatchResourceWithStatus(apiv3.KindPacketCapture, namespace1, name1, spec2, status1))

			By("Getting resource and verifying status1 is present")
			res, outError = c.PacketCaptures().Get(ctx, namespace1, name1, options.GetOptions{})
			log.Infof("After get %#v", res)
			Expect(outError).ToNot(HaveOccurred())
			Expect(res).To(MatchResourceWithStatus(apiv3.KindPacketCapture, namespace1, name1, spec2, status1))
			Expect(res.Status.Files).To(HaveLen(1))

			// Track the version of the updated name1 data.
			rv1_3 := res.ResourceVersion

			if config.Spec.DatastoreType != apiconfig.Kubernetes {
				By("Deleting PacketCapture (namespace1/name1) with the old resource version")
				_, outError = c.PacketCaptures().Delete(ctx, namespace1, name1, options.DeleteOptions{ResourceVersion: rv1_1})
				Expect(outError).To(HaveOccurred())
				Expect(outError.Error()).To(Equal("update conflict: PacketCapture(" + namespace1 + "/" + name1 + ")"))
			}

			By("Deleting PacketCapture (namespace1/name1) with the new resource version")
			dres, outError := c.PacketCaptures().Delete(ctx, namespace1, name1, options.DeleteOptions{ResourceVersion: rv1_3})
			Expect(outError).NotTo(HaveOccurred())
			Expect(dres).To(MatchResource(apiv3.KindPacketCapture, namespace1, name1, spec2))

			if config.Spec.DatastoreType != apiconfig.Kubernetes {
				By("Updating PacketCapture namespace2/name2 with a 2s TTL and waiting for the entry to be deleted")

				_, outError = c.PacketCaptures().Update(ctx, res2, options.SetOptions{TTL: 2 * time.Second})
				Expect(outError).NotTo(HaveOccurred())
				time.Sleep(1 * time.Second)
				_, outError = c.PacketCaptures().Get(ctx, namespace2, name2, options.GetOptions{})
				Expect(outError).NotTo(HaveOccurred())
				time.Sleep(2 * time.Second)
				_, outError = c.PacketCaptures().Get(ctx, namespace2, name2, options.GetOptions{})
				Expect(outError).To(HaveOccurred())
				Expect(outError.Error()).To(ContainSubstring("resource does not exist: PacketCapture(" + namespace2 + "/" + name2 + ") with error:"))

				By("Creating PacketCapture name2 with a 2s TTL and waiting for the entry to be deleted")
				_, outError = c.PacketCaptures().Create(ctx, &apiv3.PacketCapture{
					ObjectMeta: metav1.ObjectMeta{Namespace: namespace2, Name: name2},
					Spec:       spec2,
				}, options.SetOptions{TTL: 2 * time.Second})
				Expect(outError).NotTo(HaveOccurred())
				time.Sleep(1 * time.Second)
				_, outError = c.PacketCaptures().Get(ctx, namespace2, name2, options.GetOptions{})
				Expect(outError).NotTo(HaveOccurred())
				time.Sleep(2 * time.Second)
				_, outError = c.PacketCaptures().Get(ctx, namespace2, name2, options.GetOptions{})
				Expect(outError).To(HaveOccurred())
				Expect(outError.Error()).To(ContainSubstring("resource does not exist: PacketCapture(" + namespace2 + "/" + name2 + ") with error:"))
			}

			if config.Spec.DatastoreType == apiconfig.Kubernetes {
				By("Attempting to delete PacketCapture (namespace2/name2) again")
				dres, outError = c.PacketCaptures().Delete(ctx, namespace2, name2, options.DeleteOptions{})
				Expect(outError).NotTo(HaveOccurred())
				Expect(dres).To(MatchResource(apiv3.KindPacketCapture, namespace2, name2, spec2))
			}

			By("Listing all PacketCaptures and expecting no items")
			outList, outError = c.PacketCaptures().List(ctx, options.ListOptions{})
			Expect(outError).NotTo(HaveOccurred())
			Expect(outList.Items).To(HaveLen(0))

			By("Getting PacketCapture (namespace2/name2) and expecting an error")
			_, outError = c.PacketCaptures().Get(ctx, namespace2, name2, options.GetOptions{})
			Expect(outError).To(HaveOccurred())
			Expect(outError.Error()).To(ContainSubstring("resource does not exist: PacketCapture(" + namespace2 + "/" + name2 + ") with error:"))
		},

		// Test 1: Pass two fully populated PacketCaptureSpecs and expect the series of operations to succeed.
		Entry("Two fully populated PacketCaptureSpecs", name1, name2, spec1, spec2),
	)

	Describe("PacketCapture watch functionality", func() {
		It("should handle watch events for different resource versions and event types", func() {
			c, err := clientv3.New(config)
			Expect(err).NotTo(HaveOccurred())

			be, err := backend.NewClient(config)
			Expect(err).NotTo(HaveOccurred())
			be.Clean()

			By("Listing PacketCaptures with the latest resource version and checking for two results with name1/spec2 and name2/spec2")
			outList, outError := c.PacketCaptures().List(ctx, options.ListOptions{})
			Expect(outError).NotTo(HaveOccurred())
			Expect(outList.Items).To(HaveLen(0))
			rev0 := outList.ResourceVersion

			By("Configuring a PacketCapture name1/spec1 and storing the response")
			outRes1, err := c.PacketCaptures().Create(
				ctx,
				&apiv3.PacketCapture{
					ObjectMeta: metav1.ObjectMeta{Namespace: namespace1, Name: name1},
					Spec:       spec1,
				},
				options.SetOptions{},
			)
			rev1 := outRes1.ResourceVersion

			By("Configuring a PacketCapture name2/spec2 and storing the response")
			outRes2, err := c.PacketCaptures().Create(
				ctx,
				&apiv3.PacketCapture{
					ObjectMeta: metav1.ObjectMeta{Namespace: namespace2, Name: name2},
					Spec:       spec2,
				},
				options.SetOptions{},
			)

			By("Starting a watcher from revision rev1 - this should skip the first creation")
			w, err := c.PacketCaptures().Watch(ctx, options.ListOptions{ResourceVersion: rev1})
			Expect(err).NotTo(HaveOccurred())
			testWatcher1 := testutils.NewTestResourceWatch(config.Spec.DatastoreType, w)
			defer testWatcher1.Stop()

			By("Deleting res1")
			_, err = c.PacketCaptures().Delete(ctx, namespace1, name1, options.DeleteOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("Checking for two events, create res2 and delete re1")
			testWatcher1.ExpectEvents(apiv3.KindPacketCapture, []watch.Event{
				{
					Type:   watch.Added,
					Object: outRes2,
				},
				{
					Type:     watch.Deleted,
					Previous: outRes1,
				},
			})
			testWatcher1.Stop()

			By("Starting a watcher from rev0 - this should get all events")
			w, err = c.PacketCaptures().Watch(ctx, options.ListOptions{ResourceVersion: rev0})
			Expect(err).NotTo(HaveOccurred())
			testWatcher2 := testutils.NewTestResourceWatch(config.Spec.DatastoreType, w)
			defer testWatcher2.Stop()

			By("Modifying res2")
			outRes3, err := c.PacketCaptures().Update(
				ctx,
				&apiv3.PacketCapture{
					ObjectMeta: outRes2.ObjectMeta,
					Spec:       spec1,
				},
				options.SetOptions{},
			)
			Expect(err).NotTo(HaveOccurred())
			testWatcher2.ExpectEvents(apiv3.KindPacketCapture, []watch.Event{
				{
					Type:   watch.Added,
					Object: outRes1,
				},
				{
					Type:   watch.Added,
					Object: outRes2,
				},
				{
					Type:     watch.Deleted,
					Previous: outRes1,
				},
				{
					Type:     watch.Modified,
					Previous: outRes2,
					Object:   outRes3,
				},
			})
			testWatcher2.Stop()

			// Only etcdv3 supports watching a specific instance of a resource.
			if config.Spec.DatastoreType == apiconfig.EtcdV3 {
				By("Starting a watcher from rev0 watching name1 - this should get all events for name1")
				w, err = c.PacketCaptures().Watch(ctx, options.ListOptions{Name: name1, ResourceVersion: rev0})
				Expect(err).NotTo(HaveOccurred())
				testWatcher2_1 := testutils.NewTestResourceWatch(config.Spec.DatastoreType, w)
				defer testWatcher2_1.Stop()
				testWatcher2_1.ExpectEvents(apiv3.KindPacketCapture, []watch.Event{
					{
						Type:   watch.Added,
						Object: outRes1,
					},
					{
						Type:     watch.Deleted,
						Previous: outRes1,
					},
				})
				testWatcher2_1.Stop()
			}

			By("Starting a watcher not specifying a rev - expect the current snapshot")
			w, err = c.PacketCaptures().Watch(ctx, options.ListOptions{})
			Expect(err).NotTo(HaveOccurred())
			testWatcher3 := testutils.NewTestResourceWatch(config.Spec.DatastoreType, w)
			defer testWatcher3.Stop()
			testWatcher3.ExpectEvents(apiv3.KindPacketCapture, []watch.Event{
				{
					Type:   watch.Added,
					Object: outRes3,
				},
			})
			testWatcher3.Stop()

			By("Configuring PacketCapture name1/spec1 again and storing the response")
			outRes1, err = c.PacketCaptures().Create(
				ctx,
				&apiv3.PacketCapture{
					ObjectMeta: metav1.ObjectMeta{Namespace: namespace1, Name: name1},
					Spec:       spec1,
				},
				options.SetOptions{},
			)

			By("Starting a watcher not specifying a rev - expect the current snapshot")
			w, err = c.PacketCaptures().Watch(ctx, options.ListOptions{})
			Expect(err).NotTo(HaveOccurred())
			testWatcher4 := testutils.NewTestResourceWatch(config.Spec.DatastoreType, w)
			defer testWatcher4.Stop()
			testWatcher4.ExpectEventsAnyOrder(apiv3.KindPacketCapture, []watch.Event{
				{
					Type:   watch.Added,
					Object: outRes1,
				},
				{
					Type:   watch.Added,
					Object: outRes3,
				},
			})

			By("Cleaning the datastore and expecting deletion events for each configured resource (tests prefix deletes results in individual events for each key)")
			be.Clean()
			testWatcher4.ExpectEvents(apiv3.KindPacketCapture, []watch.Event{
				{
					Type:     watch.Deleted,
					Previous: outRes1,
				},
				{
					Type:     watch.Deleted,
					Previous: outRes3,
				},
			})
			testWatcher4.Stop()
		})
	})
})
