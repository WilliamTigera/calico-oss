// Copyright (c) 2020-2022 Tigera, Inc. All rights reserved.

package clientv3_test

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/ginkgo/extensions/table"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	apiv3 "github.com/tigera/api/pkg/apis/projectcalico/v3"

	"github.com/projectcalico/calico/libcalico-go/lib/apiconfig"
	"github.com/projectcalico/calico/libcalico-go/lib/backend"
	"github.com/projectcalico/calico/libcalico-go/lib/clientv3"
	"github.com/projectcalico/calico/libcalico-go/lib/options"
	"github.com/projectcalico/calico/libcalico-go/lib/testutils"
	"github.com/projectcalico/calico/libcalico-go/lib/watch"
)

var _ = testutils.E2eDatastoreDescribe("ManagedCluster tests", testutils.DatastoreAll, func(config apiconfig.CalicoAPIConfig) {
	ctx := context.Background()
	name1 := "cluster-1"
	name2 := "cluster-2"
	invalidClusterName := "cluster"
	clusterNamespace := ""
	spec1 := apiv3.ManagedClusterSpec{}
	spec2 := apiv3.ManagedClusterSpec{}

	DescribeTable("ManagedCluster e2e CRUD tests",
		func(name1, name2, clusterNamespace string, spec1, spec2 apiv3.ManagedClusterSpec) {
			c, err := clientv3.New(config)
			Expect(err).NotTo(HaveOccurred())

			be, err := backend.NewClient(config)
			Expect(err).NotTo(HaveOccurred())
			be.Clean()

			// ----------------------------------------------------------------------------------------------------
			By("Updating the ManagedCluster before it is created")
			_, outError := c.ManagedClusters().Update(ctx, &apiv3.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: name1, ResourceVersion: "1234", CreationTimestamp: metav1.Now(), UID: uid},
				Spec:       spec1,
			}, options.SetOptions{})
			Expect(outError).To(HaveOccurred())

			Expect(outError.Error()).To(ContainSubstring(fmt.Sprintf("resource does not exist: ManagedCluster(%s)", name1)))

			// ----------------------------------------------------------------------------------------------------
			By("Attempting to create a new ManagedCluster with name1/spec1 and a non-empty ResourceVersion")
			_, outError = c.ManagedClusters().Create(ctx, &apiv3.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: name1, Namespace: clusterNamespace, ResourceVersion: "12345"},
				Spec:       spec1,
			}, options.SetOptions{})
			Expect(outError).To(HaveOccurred())
			Expect(outError.Error()).To(Equal("error with field Metadata.ResourceVersion = '12345' (field must not be set for a Create request)"))

			// ----------------------------------------------------------------------------------------------------
			By("Creating a new ManagedCluster with name1/spec1")
			res1, outError := c.ManagedClusters().Create(ctx, &apiv3.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: name1, Namespace: clusterNamespace},
				Spec:       spec1,
			}, options.SetOptions{})
			Expect(outError).NotTo(HaveOccurred())
			// fmt.Println("vakumar-res1 eq %s")
			Expect(res1).To(MatchResource(apiv3.KindManagedCluster, clusterNamespace, name1, spec1))

			// Track the version of the original data for name1.
			rv1_1 := res1.ResourceVersion

			// ----------------------------------------------------------------------------------------------------
			By("Attempting to create the same ManagedCluster with name1 but with spec2")
			_, outError = c.ManagedClusters().Create(ctx, &apiv3.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: name1, Namespace: clusterNamespace},
				Spec:       spec2,
			}, options.SetOptions{})
			Expect(outError).To(HaveOccurred())
			Expect(outError.Error()).To(Equal(fmt.Sprintf("resource already exists: ManagedCluster(%s)", name1)))

			// ----------------------------------------------------------------------------------------------------
			By("Getting ManagedCluster (name1) and comparing the output against spec1")
			res, outError := c.ManagedClusters().Get(ctx, clusterNamespace, name1, options.GetOptions{})
			Expect(outError).NotTo(HaveOccurred())
			Expect(res).To(MatchResource(apiv3.KindManagedCluster, clusterNamespace, name1, spec1))
			Expect(res.ResourceVersion).To(Equal(res1.ResourceVersion))

			// ----------------------------------------------------------------------------------------------------
			By("Getting ManagedCluster (name2) before it is created")
			_, outError = c.ManagedClusters().Get(ctx, clusterNamespace, name2, options.GetOptions{})
			Expect(outError).To(HaveOccurred())
			Expect(outError.Error()).To(ContainSubstring(fmt.Sprintf("resource does not exist: ManagedCluster(%s)", name2)))

			// ----------------------------------------------------------------------------------------------------
			By("Listing all the ManagedClusters, expecting a single result with name1/spec1")
			outList, outError := c.ManagedClusters().List(ctx, options.ListOptions{})
			Expect(outError).NotTo(HaveOccurred())
			Expect(outList.Items).To(HaveLen(1))
			Expect(&outList.Items[0]).To(MatchResource(apiv3.KindManagedCluster, testutils.ExpectNoNamespace, name1, spec1))

			// ----------------------------------------------------------------------------------------------------
			By("Creating a new ManagedCluster with name2/spec2")
			res2, outError := c.ManagedClusters().Create(ctx, &apiv3.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: name2, Namespace: clusterNamespace},
				Spec:       spec2,
			}, options.SetOptions{})
			Expect(outError).NotTo(HaveOccurred())
			Expect(res2).To(MatchResource(apiv3.KindManagedCluster, testutils.ExpectNoNamespace, name2, spec2))

			// ----------------------------------------------------------------------------------------------------
			By("Getting ManagedCluster (name2) and comparing the output against spec2")
			res, outError = c.ManagedClusters().Get(ctx, clusterNamespace, name2, options.GetOptions{})
			Expect(outError).NotTo(HaveOccurred())
			Expect(res2).To(MatchResource(apiv3.KindManagedCluster, testutils.ExpectNoNamespace, name2, spec2))
			Expect(res.ResourceVersion).To(Equal(res2.ResourceVersion))

			// ----------------------------------------------------------------------------------------------------
			By("Listing all the ManagedClusters, expecting a two results with name1/spec1 and name2/spec2")
			outList, outError = c.ManagedClusters().List(ctx, options.ListOptions{Namespace: clusterNamespace})
			Expect(outError).NotTo(HaveOccurred())
			Expect(outList.Items).To(HaveLen(2))
			Expect(&outList.Items[0]).To(MatchResource(apiv3.KindManagedCluster, clusterNamespace, name1, spec1))
			Expect(&outList.Items[1]).To(MatchResource(apiv3.KindManagedCluster, clusterNamespace, name2, spec2))

			// ----------------------------------------------------------------------------------------------------
			By("Updating ManagedCluster name1 with spec2")
			res1.Spec = spec2
			res1, outError = c.ManagedClusters().Update(ctx, res1, options.SetOptions{})
			Expect(outError).NotTo(HaveOccurred())
			Expect(res1).To(MatchResource(apiv3.KindManagedCluster, clusterNamespace, name1, spec2))

			// ----------------------------------------------------------------------------------------------------
			By("Attempting to update the ManagedCluster without a Creation Timestamp")
			res, outError = c.ManagedClusters().Update(ctx, &apiv3.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: name1, Namespace: clusterNamespace, ResourceVersion: "1234", UID: uid},
				Spec:       spec1,
			}, options.SetOptions{})
			Expect(outError).To(HaveOccurred())
			Expect(res).To(BeNil())
			Expect(outError.Error()).To(Equal("error with field Metadata.CreationTimestamp = '0001-01-01 00:00:00 +0000 UTC' (field must be set for an Update request)"))

			// ----------------------------------------------------------------------------------------------------
			By("Attempting to update the ManagedCluster without a UID")
			res, outError = c.ManagedClusters().Update(ctx, &apiv3.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: name1, Namespace: clusterNamespace, ResourceVersion: "1234", CreationTimestamp: metav1.Now()},
				Spec:       spec1,
			}, options.SetOptions{})
			Expect(outError).To(HaveOccurred())
			Expect(res).To(BeNil())
			Expect(outError.Error()).To(Equal("error with field Metadata.UID = '' (field must be set for an Update request)"))

			// Track the version of the updated name1 data.
			rv1_2 := res1.ResourceVersion

			// ----------------------------------------------------------------------------------------------------
			By("Updating ManagedCluster name1 without specifying a resource version")
			res1.Spec = spec1
			res1.ObjectMeta.ResourceVersion = ""
			_, outError = c.ManagedClusters().Update(ctx, res1, options.SetOptions{})
			Expect(outError).To(HaveOccurred())
			Expect(outError.Error()).To(Equal("error with field Metadata.ResourceVersion = '' (field must be set for an Update request)"))

			// ----------------------------------------------------------------------------------------------------
			/* Disable test: specs are the same, so no update occurs for kdd backend and revisions will be the same.
			By("Updating ManagedCluster name1 using the previous resource version")
			res1.Spec = spec1
			res1.ResourceVersion = rv1_1
			_, outError = c.ManagedClusters().Update(ctx, res1, options.SetOptions{})
			Expect(outError).To(HaveOccurred())
			Expect(outError.Error()).To(Equal("update conflict: ManagedCluster(" + name1 + ")"))
			*/

			if config.Spec.DatastoreType != apiconfig.Kubernetes {
				By("Getting ManagedCluster (name1) with the original resource version and comparing the output against spec1")
				res, outError = c.ManagedClusters().Get(ctx, clusterNamespace, name1, options.GetOptions{ResourceVersion: rv1_1})
				Expect(outError).NotTo(HaveOccurred())
				Expect(res).To(MatchResource(apiv3.KindManagedCluster, clusterNamespace, name1, spec1))
				Expect(res.ResourceVersion).To(Equal(rv1_1))
			}

			// ----------------------------------------------------------------------------------------------------
			By("Getting ManagedCluster (name1) with the updated resource version and comparing the output against spec2")
			res, outError = c.ManagedClusters().Get(ctx, clusterNamespace, name1, options.GetOptions{ResourceVersion: rv1_2})
			Expect(outError).NotTo(HaveOccurred())
			Expect(res).To(MatchResource(apiv3.KindManagedCluster, clusterNamespace, name1, spec2))
			Expect(res.ResourceVersion).To(Equal(rv1_2))

			if config.Spec.DatastoreType != apiconfig.Kubernetes {
				By("Listing ManagedClusters with the original resource version and checking for a single result with name1/spec1")
				outList, outError = c.ManagedClusters().List(ctx, options.ListOptions{ResourceVersion: rv1_1})
				Expect(outError).NotTo(HaveOccurred())
				Expect(outList.Items).To(HaveLen(1))
				Expect(&outList.Items[0]).To(MatchResource(apiv3.KindManagedCluster, clusterNamespace, name1, spec1))
			}

			// ----------------------------------------------------------------------------------------------------
			By("Listing ManagedClusters with the latest resource version and checking for two results with name1/spec2 and name2/spec2")
			outList, outError = c.ManagedClusters().List(ctx, options.ListOptions{Namespace: clusterNamespace})
			Expect(outError).NotTo(HaveOccurred())
			Expect(outList.Items).To(HaveLen(2))
			Expect(&outList.Items[0]).To(MatchResource(apiv3.KindManagedCluster, clusterNamespace, name1, spec2))
			Expect(&outList.Items[1]).To(MatchResource(apiv3.KindManagedCluster, clusterNamespace, name2, spec2))

			if config.Spec.DatastoreType != apiconfig.Kubernetes {
				By("Deleting ManagedCluster (name1) with the old resource version")
				_, outError = c.ManagedClusters().Delete(ctx, clusterNamespace, name1, options.DeleteOptions{ResourceVersion: rv1_1})
				Expect(outError).To(HaveOccurred())
				Expect(outError.Error()).To(Equal("update conflict: ManagedCluster(" + name1 + ")"))
			}

			// ----------------------------------------------------------------------------------------------------
			By("Deleting ManagedCluster (name1) with the new resource version")
			dres, outError := c.ManagedClusters().Delete(ctx, clusterNamespace, name1, options.DeleteOptions{ResourceVersion: rv1_2})
			Expect(outError).NotTo(HaveOccurred())
			Expect(dres).To(MatchResource(apiv3.KindManagedCluster, clusterNamespace, name1, spec2))

			// ----------------------------------------------------------------------------------------------------
			By("Attempting to delete ManagedCluster (name2) again")
			dres, outError = c.ManagedClusters().Delete(ctx, clusterNamespace, name2, options.DeleteOptions{})
			Expect(outError).NotTo(HaveOccurred())
			Expect(dres).To(MatchResource(apiv3.KindManagedCluster, clusterNamespace, name2, spec2))

			// ----------------------------------------------------------------------------------------------------
			By("Listing all ManagedClusters and expecting no items")
			outList, outError = c.ManagedClusters().List(ctx, options.ListOptions{Namespace: clusterNamespace})
			Expect(outError).NotTo(HaveOccurred())
			Expect(outList.Items).To(HaveLen(0))

			// ----------------------------------------------------------------------------------------------------
			By("Getting ManagedCluster (name2) and expecting an error")
			_, outError = c.ManagedClusters().Get(ctx, clusterNamespace, name2, options.GetOptions{})
			Expect(outError).To(HaveOccurred())
			Expect(outError.Error()).To(ContainSubstring("resource does not exist: ManagedCluster(" + name2 + ")"))
		},

		// Pass two fully populated ManagedClusterSpec instances and expect the series of operations to succeed
		Entry("Fully populated ManagedClusterSpec instances", name1, name2, "", spec1, spec2),
	)

	Describe("ManagedCluster Create/Update", func() {
		It("should not allow cluster with name \"cluster\"", func() {
			c, err := clientv3.New(config)
			Expect(err).NotTo(HaveOccurred())

			_, outError := c.ManagedClusters().Create(ctx, &apiv3.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: invalidClusterName, Namespace: clusterNamespace, ResourceVersion: "12345"},
				Spec:       spec1,
			}, options.SetOptions{})
			Expect(outError).To(HaveOccurred())
			Expect(outError.Error()).To(ContainSubstring("Invalid name for managed cluster"))
		})

		It("should not create a managed cluster with a populated installation manifest", func() {
			c, err := clientv3.New(config)
			Expect(err).NotTo(HaveOccurred())

			_, outError := c.ManagedClusters().Create(ctx, &apiv3.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "any", Namespace: clusterNamespace},
				Spec: apiv3.ManagedClusterSpec{
					InstallationManifest: "bogus",
				},
			}, options.SetOptions{})
			Expect(outError).To(HaveOccurred())
			Expect(outError.Error()).To(ContainSubstring(clientv3.ErrMsgNotEmpty))
		})

		It("should not update a managed cluster with a populated installation manifest", func() {
			c, err := clientv3.New(config)
			Expect(err).NotTo(HaveOccurred())

			_, outError := c.ManagedClusters().Create(ctx, &apiv3.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "any", Namespace: clusterNamespace},
			}, options.SetOptions{})
			Expect(outError).NotTo(HaveOccurred())

			_, outError = c.ManagedClusters().Update(ctx, &apiv3.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "any", Namespace: clusterNamespace},
				Spec: apiv3.ManagedClusterSpec{
					InstallationManifest: "bogus",
				},
			}, options.SetOptions{})
			Expect(outError).To(HaveOccurred())
			Expect(outError.Error()).To(ContainSubstring(clientv3.ErrMsgNotEmpty))
		})
	})

	Describe("ManagedCluster watch functionality", func() {
		It("should handle watch events for different resource versions and event types", func() {
			c, err := clientv3.New(config)
			Expect(err).NotTo(HaveOccurred())

			be, err := backend.NewClient(config)
			Expect(err).NotTo(HaveOccurred())
			be.Clean()

			By("Listing ManagedClusters with the latest resource version before creating any instances")
			outList, outError := c.ManagedClusters().List(ctx, options.ListOptions{Namespace: clusterNamespace})
			Expect(outError).NotTo(HaveOccurred())
			Expect(outList.Items).To(HaveLen(0))
			rev0 := outList.ResourceVersion

			By("Configuring a ManagedCluster name1/spec1 and storing the response")
			outRes1, err := c.ManagedClusters().Create(
				ctx,
				&apiv3.ManagedCluster{
					ObjectMeta: metav1.ObjectMeta{Name: name1, Namespace: clusterNamespace},
					Spec:       spec1,
				},
				options.SetOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			rev1 := outRes1.ResourceVersion

			By("Configuring a ManagedCluster name2/spec2 and storing the response")
			outRes2, err := c.ManagedClusters().Create(
				ctx,
				&apiv3.ManagedCluster{
					ObjectMeta: metav1.ObjectMeta{Name: name2, Namespace: clusterNamespace},
					Spec:       spec2,
				},
				options.SetOptions{},
			)
			Expect(err).ToNot(HaveOccurred())

			By("Starting a watcher from revision rev1 - this should skip the first creation")
			w, err := c.ManagedClusters().Watch(ctx, options.ListOptions{ResourceVersion: rev1})
			Expect(err).NotTo(HaveOccurred())
			testWatcher1 := testutils.NewTestResourceWatch(config.Spec.DatastoreType, w)
			defer testWatcher1.Stop()

			By("Deleting res1")
			_, err = c.ManagedClusters().Delete(ctx, clusterNamespace, name1, options.DeleteOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("Checking for two events, create res2 and delete re1")
			testWatcher1.ExpectEvents(apiv3.KindManagedCluster, []watch.Event{
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
			w, err = c.ManagedClusters().Watch(ctx, options.ListOptions{ResourceVersion: rev0})
			Expect(err).NotTo(HaveOccurred())
			testWatcher2 := testutils.NewTestResourceWatch(config.Spec.DatastoreType, w)
			defer testWatcher2.Stop()

			By("Modifying res2")
			// Note that the specs are empty, so let's modify a label instead.
			m := outRes2.ObjectMeta.DeepCopy()
			m.Labels = map[string]string{
				"newlabel": "newvalue",
			}
			outRes3, err := c.ManagedClusters().Update(
				ctx,
				&apiv3.ManagedCluster{
					ObjectMeta: *m,
					Spec:       spec1,
				},
				options.SetOptions{},
			)
			Expect(err).NotTo(HaveOccurred())
			testWatcher2.ExpectEvents(apiv3.KindManagedCluster, []watch.Event{
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
				w, err = c.ManagedClusters().Watch(ctx, options.ListOptions{Name: name1, Namespace: clusterNamespace, ResourceVersion: rev0})
				Expect(err).NotTo(HaveOccurred())
				testWatcher2_1 := testutils.NewTestResourceWatch(config.Spec.DatastoreType, w)
				defer testWatcher2_1.Stop()
				testWatcher2_1.ExpectEvents(apiv3.KindManagedCluster, []watch.Event{
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
			w, err = c.ManagedClusters().Watch(ctx, options.ListOptions{})
			Expect(err).NotTo(HaveOccurred())
			testWatcher3 := testutils.NewTestResourceWatch(config.Spec.DatastoreType, w)
			defer testWatcher3.Stop()
			testWatcher3.ExpectEvents(apiv3.KindManagedCluster, []watch.Event{
				{
					Type:   watch.Added,
					Object: outRes3,
				},
			})
			testWatcher3.Stop()

			By("Configuring ManagedCluster name1/spec1 again and storing the response")
			outRes1, err = c.ManagedClusters().Create(
				ctx,
				&apiv3.ManagedCluster{
					ObjectMeta: metav1.ObjectMeta{Name: name1, Namespace: clusterNamespace},
					Spec:       spec1,
				},
				options.SetOptions{},
			)

			By("Starting a watcher not specifying a rev - expect the current snapshot")
			w, err = c.ManagedClusters().Watch(ctx, options.ListOptions{Namespace: clusterNamespace})
			Expect(err).NotTo(HaveOccurred())
			testWatcher4 := testutils.NewTestResourceWatch(config.Spec.DatastoreType, w)
			defer testWatcher4.Stop()
			testWatcher4.ExpectEventsAnyOrder(apiv3.KindManagedCluster, []watch.Event{
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
			testWatcher4.ExpectEvents(apiv3.KindManagedCluster, []watch.Event{
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
