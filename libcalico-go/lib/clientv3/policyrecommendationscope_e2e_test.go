// Copyright (c) 2019 Tigera, Inc. All rights reserved.

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
	. "github.com/onsi/ginkgo/extensions/table"
	. "github.com/onsi/gomega"
	apiv3 "github.com/tigera/api/pkg/apis/projectcalico/v3"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/projectcalico/calico/libcalico-go/lib/apiconfig"
	"github.com/projectcalico/calico/libcalico-go/lib/backend"
	"github.com/projectcalico/calico/libcalico-go/lib/clientv3"
	"github.com/projectcalico/calico/libcalico-go/lib/options"
	"github.com/projectcalico/calico/libcalico-go/lib/testutils"
	"github.com/projectcalico/calico/libcalico-go/lib/watch"
)

var _ = testutils.E2eDatastoreDescribe("PolicyRecommendationScope tests", testutils.DatastoreAll, func(config apiconfig.CalicoAPIConfig) {
	ctx := context.Background()

	defaultValue := 20
	defaultInterval := metav1.Duration{Duration: 150 * time.Second}
	defaultLookback := metav1.Duration{Duration: 24 * time.Hour}
	defaultStabilizationPeriod := metav1.Duration{Duration: 10 * time.Minute}

	alteredValue := 30
	alteredInterval := metav1.Duration{Duration: 180 * time.Second}
	alteredLookback := metav1.Duration{Duration: 48 * time.Hour}
	alteredStabilizationPeriod := metav1.Duration{Duration: 20 * time.Minute}

	name1 := "policyrecommendationscopespec-1"
	name2 := "policyrecommendationscopespec-2"

	spec1 := apiv3.PolicyRecommendationScopeSpec{
		Interval:               &defaultInterval,
		InitialLookback:        &defaultLookback,
		StabilizationPeriod:    &defaultStabilizationPeriod,
		MaxRules:               &defaultValue,
		PoliciesLearningCutOff: &defaultValue,
	}

	spec2 := apiv3.PolicyRecommendationScopeSpec{
		Interval:               &alteredInterval,
		InitialLookback:        &alteredLookback,
		StabilizationPeriod:    &alteredStabilizationPeriod,
		MaxRules:               &alteredValue,
		PoliciesLearningCutOff: &alteredValue,
	}

	DescribeTable("PolicyRecommendationScope e2e CRUD tests",
		func(name1, name2 string, spec1, spec2 apiv3.PolicyRecommendationScopeSpec) {
			c, err := clientv3.New(config)
			Expect(err).NotTo(HaveOccurred())

			be, err := backend.NewClient(config)
			Expect(err).NotTo(HaveOccurred())
			be.Clean()

			By("Updating the PolicyRecommendationScope before it is created")
			_, outError := c.PolicyRecommendationScopes().Update(ctx, &apiv3.PolicyRecommendationScope{
				ObjectMeta: metav1.ObjectMeta{Name: name1, ResourceVersion: "1234", CreationTimestamp: metav1.Now(), UID: uid},
				Spec:       spec1,
			}, options.SetOptions{})
			Expect(outError).To(HaveOccurred())

			Expect(outError.Error()).To(ContainSubstring(fmt.Sprintf("resource does not exist: PolicyRecommendationScope(%s)", name1)))

			By("Attempting to creating a new PolicyRecommendationScope with name1/spec1 and a non-empty ResourceVersion")
			_, outError = c.PolicyRecommendationScopes().Create(ctx, &apiv3.PolicyRecommendationScope{
				ObjectMeta: metav1.ObjectMeta{Name: name1, ResourceVersion: "12345"},
				Spec:       spec1,
			}, options.SetOptions{})
			Expect(outError).To(HaveOccurred())
			Expect(outError.Error()).To(Equal("error with field Metadata.ResourceVersion = '12345' (field must not be set for a Create request)"))

			By("Creating a new PolicyRecommendationScope with name1/spec1")
			res1, outError := c.PolicyRecommendationScopes().Create(ctx, &apiv3.PolicyRecommendationScope{
				ObjectMeta: metav1.ObjectMeta{Name: name1},
				Spec:       spec1,
			}, options.SetOptions{})
			Expect(outError).NotTo(HaveOccurred())
			Expect(res1).To(MatchResource(apiv3.KindPolicyRecommendationScope, testutils.ExpectNoNamespace, name1, spec1))

			// Track the version of the original data for name1.
			rv1_1 := res1.ResourceVersion

			By("Attempting to create the same PolicyRecommendationScope with name1")
			_, outError = c.PolicyRecommendationScopes().Create(ctx, &apiv3.PolicyRecommendationScope{
				ObjectMeta: metav1.ObjectMeta{Name: name1},
				Spec:       spec1,
			}, options.SetOptions{})
			Expect(outError).To(HaveOccurred())
			Expect(outError.Error()).To(Equal(fmt.Sprintf("resource already exists: PolicyRecommendationScope(%s)", name1)))

			By("Getting PolicyRecommendationScope (name1) and comparing the output against spec1")
			res, outError := c.PolicyRecommendationScopes().Get(ctx, name1, options.GetOptions{})
			Expect(outError).NotTo(HaveOccurred())
			Expect(res).To(MatchResource(apiv3.KindPolicyRecommendationScope, testutils.ExpectNoNamespace, name1, spec1))
			Expect(res.ResourceVersion).To(Equal(res1.ResourceVersion))

			By("Getting PolicyRecommendationScope (name2) before it is created")
			_, outError = c.PolicyRecommendationScopes().Get(ctx, name2, options.GetOptions{})
			Expect(outError).To(HaveOccurred())
			Expect(outError.Error()).To(ContainSubstring(fmt.Sprintf("resource does not exist: PolicyRecommendationScope(%s)", name2)))

			By("Listing all the PolicyRecommendationScopes, expecting a single result with name1/spec1")
			outList, outError := c.PolicyRecommendationScopes().List(ctx, options.ListOptions{})
			Expect(outError).NotTo(HaveOccurred())
			Expect(outList.Items).To(HaveLen(1))
			Expect(&outList.Items[0]).To(MatchResource(apiv3.KindPolicyRecommendationScope, testutils.ExpectNoNamespace, name1, spec1))

			By("Creating a new PolicyRecommendationScope with name2/spec2")
			res2, outError := c.PolicyRecommendationScopes().Create(ctx, &apiv3.PolicyRecommendationScope{
				ObjectMeta: metav1.ObjectMeta{Name: name2},
				Spec:       spec2,
			}, options.SetOptions{})
			Expect(outError).NotTo(HaveOccurred())
			Expect(res2).To(MatchResource(apiv3.KindPolicyRecommendationScope, testutils.ExpectNoNamespace, name2, spec2))

			By("Getting PolicyRecommendationScope (name2) and comparing the output against spec2")
			res, outError = c.PolicyRecommendationScopes().Get(ctx, name2, options.GetOptions{})
			Expect(outError).NotTo(HaveOccurred())
			Expect(res2).To(MatchResource(apiv3.KindPolicyRecommendationScope, testutils.ExpectNoNamespace, name2, spec2))
			Expect(res.ResourceVersion).To(Equal(res2.ResourceVersion))

			By("Listing all the PolicyRecommendationScopes, expecting a two results with name1/spec1 and name2/spec2")
			outList, outError = c.PolicyRecommendationScopes().List(ctx, options.ListOptions{})
			Expect(outError).NotTo(HaveOccurred())
			Expect(outList.Items).To(HaveLen(2))
			Expect(&outList.Items[0]).To(MatchResource(apiv3.KindPolicyRecommendationScope, testutils.ExpectNoNamespace, name1, spec1))
			Expect(&outList.Items[1]).To(MatchResource(apiv3.KindPolicyRecommendationScope, testutils.ExpectNoNamespace, name2, spec2))

			By("Updating PolicyRecommendationScope name1")
			spec1Tmp := spec2

			res1.Spec = spec1Tmp
			res1, outError = c.PolicyRecommendationScopes().Update(ctx, res1, options.SetOptions{})
			Expect(outError).NotTo(HaveOccurred())
			Expect(res1).To(MatchResource(apiv3.KindPolicyRecommendationScope, testutils.ExpectNoNamespace, name1, spec1Tmp))

			By("Attempting to update the PolicyRecommendationScope without a Creation Timestamp")
			res, outError = c.PolicyRecommendationScopes().Update(ctx, &apiv3.PolicyRecommendationScope{
				ObjectMeta: metav1.ObjectMeta{Name: name1, ResourceVersion: "1234", UID: uid},
				Spec:       spec1,
			}, options.SetOptions{})
			Expect(outError).To(HaveOccurred())
			Expect(res).To(BeNil())
			Expect(outError.Error()).To(Equal("error with field Metadata.CreationTimestamp = '0001-01-01 00:00:00 +0000 UTC' (field must be set for an Update request)"))

			By("Attempting to update the PolicyRecommendationScope without a UID")
			res, outError = c.PolicyRecommendationScopes().Update(ctx, &apiv3.PolicyRecommendationScope{
				ObjectMeta: metav1.ObjectMeta{Name: name1, ResourceVersion: "1234", CreationTimestamp: metav1.Now()},
				Spec:       spec1,
			}, options.SetOptions{})
			Expect(outError).To(HaveOccurred())
			Expect(res).To(BeNil())
			Expect(outError.Error()).To(Equal("error with field Metadata.UID = '' (field must be set for an Update request)"))

			// Track the version of the updated name1 data.
			rv1_2 := res1.ResourceVersion

			By("Updating PolicyRecommendationScope name1 without specifying a resource version")
			res1.Spec = spec1
			res1.ObjectMeta.ResourceVersion = ""
			_, outError = c.PolicyRecommendationScopes().Update(ctx, res1, options.SetOptions{})
			Expect(outError).To(HaveOccurred())
			Expect(outError.Error()).To(Equal("error with field Metadata.ResourceVersion = '' (field must be set for an Update request)"))

			By("Updating PolicyRecommendationScope name1 using the previous resource version")
			res1.Spec = spec1
			res1.ResourceVersion = rv1_1
			_, outError = c.PolicyRecommendationScopes().Update(ctx, res1, options.SetOptions{})
			Expect(outError).To(HaveOccurred())
			Expect(outError.Error()).To(Equal("update conflict: PolicyRecommendationScope(" + name1 + ")"))

			if config.Spec.DatastoreType != apiconfig.Kubernetes {
				By("Getting PolicyRecommendationScope (name1) with the original resource version and comparing the output against spec1")
				res, outError = c.PolicyRecommendationScopes().Get(ctx, name1, options.GetOptions{ResourceVersion: rv1_1})
				Expect(outError).NotTo(HaveOccurred())
				Expect(res).To(MatchResource(apiv3.KindPolicyRecommendationScope, testutils.ExpectNoNamespace, name1, spec1))
				Expect(res.ResourceVersion).To(Equal(rv1_1))
			}

			By("Getting PolicyRecommendationScope (name1) with the updated resource version and comparing the output against spec2")
			res, outError = c.PolicyRecommendationScopes().Get(ctx, name1, options.GetOptions{ResourceVersion: rv1_2})
			Expect(outError).NotTo(HaveOccurred())
			Expect(res).To(MatchResource(apiv3.KindPolicyRecommendationScope, testutils.ExpectNoNamespace, name1, spec1Tmp))
			Expect(res.ResourceVersion).To(Equal(rv1_2))

			if config.Spec.DatastoreType != apiconfig.Kubernetes {
				By("Listing PolicyRecommendationScopes with the original resource version and checking for a single result with name1/spec1")
				outList, outError = c.PolicyRecommendationScopes().List(ctx, options.ListOptions{ResourceVersion: rv1_1})
				Expect(outError).NotTo(HaveOccurred())
				Expect(outList.Items).To(HaveLen(1))
				Expect(&outList.Items[0]).To(MatchResource(apiv3.KindPolicyRecommendationScope, testutils.ExpectNoNamespace, name1, spec1))
			}

			By("Listing PolicyRecommendationScopes with the latest resource version and checking for two results with name1/spec2 and name2/spec2")
			outList, outError = c.PolicyRecommendationScopes().List(ctx, options.ListOptions{})
			Expect(outError).NotTo(HaveOccurred())
			Expect(outList.Items).To(HaveLen(2))
			Expect(&outList.Items[0]).To(MatchResource(apiv3.KindPolicyRecommendationScope, testutils.ExpectNoNamespace, name1, spec1Tmp))
			Expect(&outList.Items[1]).To(MatchResource(apiv3.KindPolicyRecommendationScope, testutils.ExpectNoNamespace, name2, spec2))

			if config.Spec.DatastoreType != apiconfig.Kubernetes {
				By("Deleting PolicyRecommendationScope (name1) with the old resource version")
				_, outError = c.PolicyRecommendationScopes().Delete(ctx, name1, options.DeleteOptions{ResourceVersion: rv1_1})
				Expect(outError).To(HaveOccurred())
				Expect(outError.Error()).To(Equal("update conflict: PolicyRecommendationScope(" + name1 + ")"))
			}

			By("Deleting PolicyRecommendationScope (name1) with the new resource version")
			dres, outError := c.PolicyRecommendationScopes().Delete(ctx, name1, options.DeleteOptions{ResourceVersion: rv1_2})
			Expect(outError).NotTo(HaveOccurred())
			Expect(dres).To(MatchResource(apiv3.KindPolicyRecommendationScope, testutils.ExpectNoNamespace, name1, spec1Tmp))

			if config.Spec.DatastoreType != apiconfig.Kubernetes {
				By("Updating PolicyRecommendationScope name2 with a 2s TTL and waiting for the entry to be deleted")
				_, outError = c.PolicyRecommendationScopes().Update(ctx, res2, options.SetOptions{TTL: 2 * time.Second})
				Expect(outError).NotTo(HaveOccurred())
				time.Sleep(1 * time.Second)
				_, outError = c.PolicyRecommendationScopes().Get(ctx, name2, options.GetOptions{})
				Expect(outError).NotTo(HaveOccurred())
				time.Sleep(2 * time.Second)
				_, outError = c.PolicyRecommendationScopes().Get(ctx, name2, options.GetOptions{})
				Expect(outError).To(HaveOccurred())
				Expect(outError.Error()).To(ContainSubstring("resource does not exist: PolicyRecommendationScope(" + name2 + ")"))

				By("Creating PolicyRecommendationScope name2 with a 2s TTL and waiting for the entry to be deleted")
				_, outError = c.PolicyRecommendationScopes().Create(ctx, &apiv3.PolicyRecommendationScope{
					ObjectMeta: metav1.ObjectMeta{Name: name2},
					Spec:       spec2,
				}, options.SetOptions{TTL: 2 * time.Second})
				Expect(outError).NotTo(HaveOccurred())
				time.Sleep(1 * time.Second)
				_, outError = c.PolicyRecommendationScopes().Get(ctx, name2, options.GetOptions{})
				Expect(outError).NotTo(HaveOccurred())
				time.Sleep(2 * time.Second)
				_, outError = c.PolicyRecommendationScopes().Get(ctx, name2, options.GetOptions{})
				Expect(outError).To(HaveOccurred())
				Expect(outError.Error()).To(ContainSubstring("resource does not exist: PolicyRecommendationScope(" + name2 + ")"))
			}

			if config.Spec.DatastoreType == apiconfig.Kubernetes {
				By("Attempting to delete PolicyRecommendationScope (name2) again")
				dres, outError = c.PolicyRecommendationScopes().Delete(ctx, name2, options.DeleteOptions{})
				Expect(outError).NotTo(HaveOccurred())
				Expect(dres).To(MatchResource(apiv3.KindPolicyRecommendationScope, testutils.ExpectNoNamespace, name2, spec2))
			}

			By("Listing all PolicyRecommendationScopes and expecting no items")
			outList, outError = c.PolicyRecommendationScopes().List(ctx, options.ListOptions{})
			Expect(outError).NotTo(HaveOccurred())
			Expect(outList.Items).To(HaveLen(0))

			By("Getting PolicyRecommendationScope (name2) and expecting an error")
			_, outError = c.PolicyRecommendationScopes().Get(ctx, name2, options.GetOptions{})
			Expect(outError).To(HaveOccurred())
			Expect(outError.Error()).To(ContainSubstring("resource does not exist: PolicyRecommendationScope(" + name2 + ")"))
		},

		// Test 1: Pass two fully populated PolicyRecommendationScopeSpecs and expect the series of operations to succeed.
		Entry("Two fully populated PolicyRecommendationScopeSpecs", name1, name2, spec1, spec2),
	)

	Describe("PolicyRecommendationScope watch functionality", func() {
		It("should handle watch events for different resource versions and event types", func() {
			c, err := clientv3.New(config)
			Expect(err).NotTo(HaveOccurred())

			be, err := backend.NewClient(config)
			Expect(err).NotTo(HaveOccurred())
			be.Clean()

			By("Listing PolicyRecommendationScopes with the latest resource version and checking for two results with name1/spec2 and name2/spec2")
			outList, outError := c.PolicyRecommendationScopes().List(ctx, options.ListOptions{})
			Expect(outError).NotTo(HaveOccurred())
			Expect(outList.Items).To(HaveLen(0))
			rev0 := outList.ResourceVersion

			By("Configuring a PolicyRecommendationScope name1/spec1 and storing the response")
			outRes1, err := c.PolicyRecommendationScopes().Create(
				ctx,
				&apiv3.PolicyRecommendationScope{
					ObjectMeta: metav1.ObjectMeta{Name: name1},
					Spec:       spec1,
				},
				options.SetOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			rev1 := outRes1.ResourceVersion

			By("Configuring a PolicyRecommendationScope name2/spec2 and storing the response")
			outRes2, err := c.PolicyRecommendationScopes().Create(
				ctx,
				&apiv3.PolicyRecommendationScope{
					ObjectMeta: metav1.ObjectMeta{Name: name2},
					Spec:       spec2,
				},
				options.SetOptions{},
			)
			Expect(err).ToNot(HaveOccurred())

			By("Starting a watcher from revision rev1 - this should skip the first creation")
			w, err := c.PolicyRecommendationScopes().Watch(ctx, options.ListOptions{ResourceVersion: rev1})
			Expect(err).NotTo(HaveOccurred())
			testWatcher1 := testutils.NewTestResourceWatch(config.Spec.DatastoreType, w)
			defer testWatcher1.Stop()

			By("Deleting res1")
			_, err = c.PolicyRecommendationScopes().Delete(ctx, name1, options.DeleteOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("Checking for two events, create res2 and delete re1")
			testWatcher1.ExpectEvents(apiv3.KindPolicyRecommendationScope, []watch.Event{
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
			w, err = c.PolicyRecommendationScopes().Watch(ctx, options.ListOptions{ResourceVersion: rev0})
			Expect(err).NotTo(HaveOccurred())
			testWatcher2 := testutils.NewTestResourceWatch(config.Spec.DatastoreType, w)
			defer testWatcher2.Stop()

			By("Modifying res2")
			spec2Tmp := spec2
			modifiedInterval := metav1.Duration{Duration: 210 * time.Second}
			spec2Tmp.Interval = &modifiedInterval
			outRes3, err := c.PolicyRecommendationScopes().Update(
				ctx,
				&apiv3.PolicyRecommendationScope{
					ObjectMeta: outRes2.ObjectMeta,
					Spec:       spec2Tmp,
				},
				options.SetOptions{},
			)
			Expect(err).NotTo(HaveOccurred())
			testWatcher2.ExpectEvents(apiv3.KindPolicyRecommendationScope, []watch.Event{
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
				w, err = c.PolicyRecommendationScopes().Watch(ctx, options.ListOptions{Name: name1, ResourceVersion: rev0})
				Expect(err).NotTo(HaveOccurred())
				testWatcher2_1 := testutils.NewTestResourceWatch(config.Spec.DatastoreType, w)
				defer testWatcher2_1.Stop()
				testWatcher2_1.ExpectEvents(apiv3.KindPolicyRecommendationScope, []watch.Event{
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
			w, err = c.PolicyRecommendationScopes().Watch(ctx, options.ListOptions{})
			Expect(err).NotTo(HaveOccurred())
			testWatcher3 := testutils.NewTestResourceWatch(config.Spec.DatastoreType, w)
			defer testWatcher3.Stop()
			testWatcher3.ExpectEvents(apiv3.KindPolicyRecommendationScope, []watch.Event{
				{
					Type:   watch.Added,
					Object: outRes3,
				},
			})
			testWatcher3.Stop()

			By("Configuring PolicyRecommendationScope name1/spec1 again and storing the response")
			outRes1, err = c.PolicyRecommendationScopes().Create(
				ctx,
				&apiv3.PolicyRecommendationScope{
					ObjectMeta: metav1.ObjectMeta{Name: name1},
					Spec:       spec1,
				},
				options.SetOptions{},
			)
			Expect(err).NotTo(HaveOccurred())

			By("Starting a watcher not specifying a rev - expect the current snapshot")
			w, err = c.PolicyRecommendationScopes().Watch(ctx, options.ListOptions{})
			Expect(err).NotTo(HaveOccurred())
			testWatcher4 := testutils.NewTestResourceWatch(config.Spec.DatastoreType, w)
			defer testWatcher4.Stop()
			testWatcher4.ExpectEventsAnyOrder(apiv3.KindPolicyRecommendationScope, []watch.Event{
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
			testWatcher4.ExpectEvents(apiv3.KindPolicyRecommendationScope, []watch.Event{
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
