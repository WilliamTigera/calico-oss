// Copyright (c) 2023 Tigera, Inc. All rights reserved.

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

package clientv3_test

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	. "github.com/onsi/ginkgo/extensions/table"
	apiv3 "github.com/tigera/api/pkg/apis/projectcalico/v3"

	"github.com/projectcalico/calico/libcalico-go/lib/apiconfig"
	"github.com/projectcalico/calico/libcalico-go/lib/backend"
	"github.com/projectcalico/calico/libcalico-go/lib/clientv3"
	"github.com/projectcalico/calico/libcalico-go/lib/options"
	"github.com/projectcalico/calico/libcalico-go/lib/testutils"
	"github.com/projectcalico/calico/libcalico-go/lib/watch"
)

var _ = testutils.E2eDatastoreDescribe("SecuritEventWebhook tests", testutils.DatastoreAll, func(config apiconfig.CalicoAPIConfig) {
	ctx := context.Background()
	name1 := "security-event-webhook-1"
	name2 := "security-event-webhook-2"
	spec1 := apiv3.SecurityEventWebhookSpec{
		Consumer: "Slack",
		State:    "Enabled",
		Query:    "selector-1",
		Config:   []apiv3.SecurityEventWebhookConfigVar{},
	}
	spec2 := apiv3.SecurityEventWebhookSpec{
		Consumer: "Jira",
		State:    "Disabled",
		Query:    "selector-2",
		Config:   []apiv3.SecurityEventWebhookConfigVar{},
	}

	DescribeTable("SecurityEventWebhook e2e CRUD tests",
		func(name1, name2 string, spec1, spec2 apiv3.SecurityEventWebhookSpec) {
			c, err := clientv3.New(config)
			Expect(err).NotTo(HaveOccurred())

			be, err := backend.NewClient(config)
			Expect(err).NotTo(HaveOccurred())
			be.Clean()

			By("Updating the SecurityEventWebhook before it is created")
			_, outError := c.SecurityEventWebhook().Update(ctx, &apiv3.SecurityEventWebhook{
				ObjectMeta: metav1.ObjectMeta{Name: name1, ResourceVersion: "1234", CreationTimestamp: metav1.Now(), UID: uid},
				Spec:       spec1,
			}, options.SetOptions{})
			Expect(outError).To(HaveOccurred())
			Expect(outError.Error()).To(ContainSubstring(fmt.Sprintf("resource does not exist: SecurityEventWebhook(%s)", name1)))

			By("Attempting to creating a new SecurityEventWebhook with name1/spec1 and a non-empty ResourceVersion")
			_, outError = c.SecurityEventWebhook().Create(ctx, &apiv3.SecurityEventWebhook{
				ObjectMeta: metav1.ObjectMeta{Name: name1, ResourceVersion: "12345"},
				Spec:       spec1,
			}, options.SetOptions{})
			Expect(outError).To(HaveOccurred())
			Expect(outError.Error()).To(Equal("error with field Metadata.ResourceVersion = '12345' (field must not be set for a Create request)"))

			By("Creating a new SecurityEventWebhook with name1/spec1")
			res1, outError := c.SecurityEventWebhook().Create(ctx, &apiv3.SecurityEventWebhook{
				ObjectMeta: metav1.ObjectMeta{Name: name1},
				Spec:       spec1,
			}, options.SetOptions{})
			Expect(outError).NotTo(HaveOccurred())
			Expect(res1).To(MatchResource(apiv3.KindSecurityEventWebhook, testutils.ExpectNoNamespace, name1, spec1))

			// Track the version of the original data for name1.
			rv1_1 := res1.ResourceVersion

			By("Attempting to create the same SecurityEventWebhook with name1 but with spec2")
			_, outError = c.SecurityEventWebhook().Create(ctx, &apiv3.SecurityEventWebhook{
				ObjectMeta: metav1.ObjectMeta{Name: name1},
				Spec:       spec2,
			}, options.SetOptions{})
			Expect(outError).To(HaveOccurred())
			Expect(outError.Error()).To(Equal(fmt.Sprintf("resource already exists: SecurityEventWebhook(%s)", name1)))

			By("Getting SecurityEventWebhook (name1) and comparing the output against spec1")
			res, outError := c.SecurityEventWebhook().Get(ctx, name1, options.GetOptions{})
			Expect(outError).NotTo(HaveOccurred())
			Expect(res).To(MatchResource(apiv3.KindSecurityEventWebhook, testutils.ExpectNoNamespace, name1, spec1))
			Expect(res.ResourceVersion).To(Equal(res1.ResourceVersion))

			By("Getting SecurityEventWebhook (name2) before it is created")
			_, outError = c.SecurityEventWebhook().Get(ctx, name2, options.GetOptions{})
			Expect(outError).To(HaveOccurred())
			Expect(outError.Error()).To(ContainSubstring(fmt.Sprintf("resource does not exist: SecurityEventWebhook(%s)", name2)))

			By("Listing all the SecurityEventWebhook, expecting a single result with name1/spec1")
			outList, outError := c.SecurityEventWebhook().List(ctx, options.ListOptions{})
			Expect(outError).NotTo(HaveOccurred())
			Expect(outList.Items).To(HaveLen(1))
			Expect(&outList.Items[0]).To(MatchResource(apiv3.KindSecurityEventWebhook, testutils.ExpectNoNamespace, name1, spec1))

			By("Creating a new SecurityEventWebhook with name2/spec2")
			res2, outError := c.SecurityEventWebhook().Create(ctx, &apiv3.SecurityEventWebhook{
				ObjectMeta: metav1.ObjectMeta{Name: name2},
				Spec:       spec2,
			}, options.SetOptions{})
			Expect(outError).NotTo(HaveOccurred())
			Expect(res2).To(MatchResource(apiv3.KindSecurityEventWebhook, testutils.ExpectNoNamespace, name2, spec2))

			By("Getting SecurityEventWebhook (name2) and comparing the output against spec2")
			res, outError = c.SecurityEventWebhook().Get(ctx, name2, options.GetOptions{})
			Expect(outError).NotTo(HaveOccurred())
			Expect(res2).To(MatchResource(apiv3.KindSecurityEventWebhook, testutils.ExpectNoNamespace, name2, spec2))
			Expect(res.ResourceVersion).To(Equal(res2.ResourceVersion))

			By("Listing all the SecurityEventWebhook, expecting a two results with name1/spec1 and name2/spec2")
			outList, outError = c.SecurityEventWebhook().List(ctx, options.ListOptions{})
			Expect(outError).NotTo(HaveOccurred())
			Expect(outList.Items).To(HaveLen(2))
			Expect(&outList.Items[0]).To(MatchResource(apiv3.KindSecurityEventWebhook, testutils.ExpectNoNamespace, name1, spec1))
			Expect(&outList.Items[1]).To(MatchResource(apiv3.KindSecurityEventWebhook, testutils.ExpectNoNamespace, name2, spec2))

			By("Updating SecurityEventWebhook name1 with spec2")
			res1.Spec = spec2
			res1, outError = c.SecurityEventWebhook().Update(ctx, res1, options.SetOptions{})
			Expect(outError).NotTo(HaveOccurred())
			Expect(res1).To(MatchResource(apiv3.KindSecurityEventWebhook, testutils.ExpectNoNamespace, name1, spec2))

			By("Attempting to update the SecuritEventWebhook without a Creation Timestamp")
			res, outError = c.SecurityEventWebhook().Update(ctx, &apiv3.SecurityEventWebhook{
				ObjectMeta: metav1.ObjectMeta{Name: name1, ResourceVersion: "1234", UID: uid},
				Spec:       spec1,
			}, options.SetOptions{})
			Expect(outError).To(HaveOccurred())
			Expect(res).To(BeNil())
			Expect(outError.Error()).To(Equal("error with field Metadata.CreationTimestamp = '0001-01-01 00:00:00 +0000 UTC' (field must be set for an Update request)"))

			By("Attempting to update the SecurityEventWebhook without a UID")
			res, outError = c.SecurityEventWebhook().Update(ctx, &apiv3.SecurityEventWebhook{
				ObjectMeta: metav1.ObjectMeta{Name: name1, ResourceVersion: "1234", CreationTimestamp: metav1.Now()},
				Spec:       spec1,
			}, options.SetOptions{})
			Expect(outError).To(HaveOccurred())
			Expect(res).To(BeNil())
			Expect(outError.Error()).To(Equal("error with field Metadata.UID = '' (field must be set for an Update request)"))

			// Track the version of the updated name1 data.
			rv1_2 := res1.ResourceVersion

			By("Updating SecurityEventWebhook name1 without specifying a resource version")
			res1.Spec = spec1
			res1.ObjectMeta.ResourceVersion = ""
			_, outError = c.SecurityEventWebhook().Update(ctx, res1, options.SetOptions{})
			Expect(outError).To(HaveOccurred())
			Expect(outError.Error()).To(Equal("error with field Metadata.ResourceVersion = '' (field must be set for an Update request)"))

			By("Updating SecurityEventWebhook name1 using the previous resource version")
			res1.Spec = spec1
			res1.ResourceVersion = rv1_1
			_, outError = c.SecurityEventWebhook().Update(ctx, res1, options.SetOptions{})
			Expect(outError).To(HaveOccurred())
			Expect(outError.Error()).To(Equal("update conflict: SecurityEventWebhook(" + name1 + ")"))

			if config.Spec.DatastoreType != apiconfig.Kubernetes {
				By("Getting SecurityEventWebhook (name1) with the original resource version and comparing the output against spec1")
				res, outError = c.SecurityEventWebhook().Get(ctx, name1, options.GetOptions{ResourceVersion: rv1_1})
				Expect(outError).NotTo(HaveOccurred())
				Expect(res).To(MatchResource(apiv3.KindSecurityEventWebhook, testutils.ExpectNoNamespace, name1, spec1))
				Expect(res.ResourceVersion).To(Equal(rv1_1))
			}

			By("Getting SecurityEventWebhook (name1) with the updated resource version and comparing the output against spec2")
			res, outError = c.SecurityEventWebhook().Get(ctx, name1, options.GetOptions{ResourceVersion: rv1_2})
			Expect(outError).NotTo(HaveOccurred())
			Expect(res).To(MatchResource(apiv3.KindSecurityEventWebhook, testutils.ExpectNoNamespace, name1, spec2))
			Expect(res.ResourceVersion).To(Equal(rv1_2))

			if config.Spec.DatastoreType != apiconfig.Kubernetes {
				By("Listing SecurityEventWebhook with the original resource version and checking for a single result with name1/spec1")
				outList, outError = c.SecurityEventWebhook().List(ctx, options.ListOptions{ResourceVersion: rv1_1})
				Expect(outError).NotTo(HaveOccurred())
				Expect(outList.Items).To(HaveLen(1))
				Expect(&outList.Items[0]).To(MatchResource(apiv3.KindSecurityEventWebhook, testutils.ExpectNoNamespace, name1, spec1))
			}

			By("Listing SecurityEventWebhook with the latest resource version and checking for two results with name1/spec2 and name2/spec2")
			outList, outError = c.SecurityEventWebhook().List(ctx, options.ListOptions{})
			Expect(outError).NotTo(HaveOccurred())
			Expect(outList.Items).To(HaveLen(2))
			Expect(&outList.Items[0]).To(MatchResource(apiv3.KindSecurityEventWebhook, testutils.ExpectNoNamespace, name1, spec2))
			Expect(&outList.Items[1]).To(MatchResource(apiv3.KindSecurityEventWebhook, testutils.ExpectNoNamespace, name2, spec2))

			if config.Spec.DatastoreType != apiconfig.Kubernetes {
				By("Deleting SecurityEventWebhook (name1) with the old resource version")
				_, outError = c.SecurityEventWebhook().Delete(ctx, name1, options.DeleteOptions{ResourceVersion: rv1_1})
				Expect(outError).To(HaveOccurred())
				Expect(outError.Error()).To(Equal("update conflict: SecurityEventWebhook(" + name1 + ")"))
			}

			By("Deleting SecurityEventWebhook (name1) with the new resource version")
			dres, outError := c.SecurityEventWebhook().Delete(ctx, name1, options.DeleteOptions{ResourceVersion: rv1_2})
			Expect(outError).NotTo(HaveOccurred())
			Expect(dres).To(MatchResource(apiv3.KindSecurityEventWebhook, testutils.ExpectNoNamespace, name1, spec2))

			if config.Spec.DatastoreType != apiconfig.Kubernetes {
				By("Updating SecurityEventWebhook name2 with a 2s TTL and waiting for the entry to be deleted")
				_, outError = c.SecurityEventWebhook().Update(ctx, res2, options.SetOptions{TTL: 2 * time.Second})
				Expect(outError).NotTo(HaveOccurred())
				time.Sleep(1 * time.Second)
				_, outError = c.SecurityEventWebhook().Get(ctx, name2, options.GetOptions{})
				Expect(outError).NotTo(HaveOccurred())
				time.Sleep(2 * time.Second)
				_, outError = c.SecurityEventWebhook().Get(ctx, name2, options.GetOptions{})
				Expect(outError).To(HaveOccurred())
				Expect(outError.Error()).To(ContainSubstring("resource does not exist: SecurityEventWebhook(" + name2 + ")"))

				By("Creating SecurityEventWebhook name2 with a 2s TTL and waiting for the entry to be deleted")
				_, outError = c.SecurityEventWebhook().Create(ctx, &apiv3.SecurityEventWebhook{
					ObjectMeta: metav1.ObjectMeta{Name: name2},
					Spec:       spec2,
				}, options.SetOptions{TTL: 2 * time.Second})
				Expect(outError).NotTo(HaveOccurred())
				time.Sleep(1 * time.Second)
				_, outError = c.SecurityEventWebhook().Get(ctx, name2, options.GetOptions{})
				Expect(outError).NotTo(HaveOccurred())
				time.Sleep(2 * time.Second)
				_, outError = c.SecurityEventWebhook().Get(ctx, name2, options.GetOptions{})
				Expect(outError).To(HaveOccurred())
				Expect(outError.Error()).To(ContainSubstring("resource does not exist: SecurityEventWebhook(" + name2 + ")"))
			}

			if config.Spec.DatastoreType == apiconfig.Kubernetes {
				By("Attempting to delete SecurityEventWebhook (name2) again")
				dres, outError = c.SecurityEventWebhook().Delete(ctx, name2, options.DeleteOptions{})
				Expect(outError).NotTo(HaveOccurred())
				Expect(dres).To(MatchResource(apiv3.KindSecurityEventWebhook, testutils.ExpectNoNamespace, name2, spec2))
			}

			By("Listing all SecurityEventWebhook and expecting no items")
			outList, outError = c.SecurityEventWebhook().List(ctx, options.ListOptions{})
			Expect(outError).NotTo(HaveOccurred())
			Expect(outList.Items).To(HaveLen(0))

			By("Getting SecurityEventWebhook (name2) and expecting an error")
			_, outError = c.SecurityEventWebhook().Get(ctx, name2, options.GetOptions{})
			Expect(outError).To(HaveOccurred())
			Expect(outError.Error()).To(ContainSubstring("resource does not exist: SecurityEventWebhook(" + name2 + ")"))
		},
		Entry("Two fully populated SecurityEventWebhookSpecs", name1, name2, spec1, spec2),
	)

	Describe("SecurityEventWebhook watch functionality", func() {
		It("should handle watch events for different resource versions and event types", func() {
			c, err := clientv3.New(config)
			Expect(err).NotTo(HaveOccurred())

			be, err := backend.NewClient(config)
			Expect(err).NotTo(HaveOccurred())
			be.Clean()

			By("Listing SecurityEventWebhooks with the latest resource version and checking for two results with name1/spec2 and name2/spec2")
			outList, outError := c.SecurityEventWebhook().List(ctx, options.ListOptions{})
			Expect(outError).NotTo(HaveOccurred())
			Expect(outList.Items).To(HaveLen(0))
			rev0 := outList.ResourceVersion

			By("Configuring a SecurityEventWebhook name1/spec1 and storing the response")
			outRes1, err := c.SecurityEventWebhook().Create(
				ctx,
				&apiv3.SecurityEventWebhook{
					ObjectMeta: metav1.ObjectMeta{Name: name1},
					Spec:       spec1,
				},
				options.SetOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			rev1 := outRes1.ResourceVersion

			By("Configuring a SecurityEventWebhook name2/spec2 and storing the response")
			outRes2, err := c.SecurityEventWebhook().Create(
				ctx,
				&apiv3.SecurityEventWebhook{
					ObjectMeta: metav1.ObjectMeta{Name: name2},
					Spec:       spec2,
				},
				options.SetOptions{},
			)
			Expect(err).ToNot(HaveOccurred())

			By("Starting a watcher from revision rev1 - this should skip the first creation")
			w, err := c.SecurityEventWebhook().Watch(ctx, options.ListOptions{ResourceVersion: rev1})
			Expect(err).NotTo(HaveOccurred())
			testWatcher1 := testutils.NewTestResourceWatch(config.Spec.DatastoreType, w)
			defer testWatcher1.Stop()

			By("Deleting res1")
			_, err = c.SecurityEventWebhook().Delete(ctx, name1, options.DeleteOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("Checking for two events, create res2 and delete re1")
			testWatcher1.ExpectEvents(apiv3.KindSecurityEventWebhook, []watch.Event{
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
			w, err = c.SecurityEventWebhook().Watch(ctx, options.ListOptions{ResourceVersion: rev0})
			Expect(err).NotTo(HaveOccurred())
			testWatcher2 := testutils.NewTestResourceWatch(config.Spec.DatastoreType, w)
			defer testWatcher2.Stop()

			By("Modifying res2")
			outRes3, err := c.SecurityEventWebhook().Update(
				ctx,
				&apiv3.SecurityEventWebhook{
					ObjectMeta: outRes2.ObjectMeta,
					Spec:       spec1,
				},
				options.SetOptions{},
			)
			Expect(err).NotTo(HaveOccurred())
			testWatcher2.ExpectEvents(apiv3.KindSecurityEventWebhook, []watch.Event{
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
				w, err = c.SecurityEventWebhook().Watch(ctx, options.ListOptions{Name: name1, ResourceVersion: rev0})
				Expect(err).NotTo(HaveOccurred())
				testWatcher2_1 := testutils.NewTestResourceWatch(config.Spec.DatastoreType, w)
				defer testWatcher2_1.Stop()
				testWatcher2_1.ExpectEvents(apiv3.KindSecurityEventWebhook, []watch.Event{
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
			w, err = c.SecurityEventWebhook().Watch(ctx, options.ListOptions{})
			Expect(err).NotTo(HaveOccurred())
			testWatcher3 := testutils.NewTestResourceWatch(config.Spec.DatastoreType, w)
			defer testWatcher3.Stop()
			testWatcher3.ExpectEvents(apiv3.KindSecurityEventWebhook, []watch.Event{
				{
					Type:   watch.Added,
					Object: outRes3,
				},
			})
			testWatcher3.Stop()

			By("Configuring SecurityEventWebhook name1/spec1 again and storing the response")
			outRes1, err = c.SecurityEventWebhook().Create(
				ctx,
				&apiv3.SecurityEventWebhook{
					ObjectMeta: metav1.ObjectMeta{Name: name1},
					Spec:       spec1,
				},
				options.SetOptions{},
			)
			Expect(err).NotTo(HaveOccurred())

			By("Starting a watcher not specifying a rev - expect the current snapshot")
			w, err = c.SecurityEventWebhook().Watch(ctx, options.ListOptions{})
			Expect(err).NotTo(HaveOccurred())
			testWatcher4 := testutils.NewTestResourceWatch(config.Spec.DatastoreType, w)
			defer testWatcher4.Stop()
			testWatcher4.ExpectEventsAnyOrder(apiv3.KindSecurityEventWebhook, []watch.Event{
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
			testWatcher4.ExpectEvents(apiv3.KindSecurityEventWebhook, []watch.Event{
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
