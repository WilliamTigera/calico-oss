// Copyright 2019 Tigera Inc. All rights reserved.

package elastic

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/tigera/intrusion-detection/controller/pkg/controller"

	"github.com/olivere/elastic/v7"
	. "github.com/onsi/gomega"
	v3 "github.com/projectcalico/libcalico-go/lib/apis/v3"

	"github.com/tigera/intrusion-detection/controller/pkg/db"
	"github.com/tigera/intrusion-detection/controller/pkg/feeds/statser"
)

// In order to run these tests against different kinds of controllers, which are
// different types, we use reflection to handle the controller (UUT) and the set
// type it will accept on its Add method.

type testCase struct {
	name    string
	makeUUT func(d interface{}) reflect.Value
	set     reflect.Value
}

var cases = []testCase{
	{
		name: "IPSet",
		makeUUT: func(d interface{}) reflect.Value {
			return reflect.ValueOf(NewIPSetController(d.(db.IPSet)))
		},
		set: reflect.ValueOf(db.IPSetSpec{"1.2.3.4"}),
	},
	{
		name: "DomainNameSet",
		makeUUT: func(d interface{}) reflect.Value {
			return reflect.ValueOf(NewDomainNameSetController(d.(db.DomainNameSet)))
		},
		set: reflect.ValueOf(db.DomainNameSetSpec{"evilstuff.bad"}),
	},
}

// The following are convenience functions to make it easier to call the Add, Run, Delete, StartReconciliation, and NoGC
// methods on the UUT, which is a reflect.Value containing the actual controller type.

func add(uut reflect.Value, ctx context.Context, name string, set reflect.Value, fail func(error), stat statser.Statser) {
	uut.MethodByName("Add").Call([]reflect.Value{
		reflect.ValueOf(ctx),
		reflect.ValueOf(name),
		set,
		reflect.ValueOf(fail),
		reflect.ValueOf(stat),
	})
}

func run(uut reflect.Value, ctx context.Context) {
	uut.MethodByName("Run").Call([]reflect.Value{reflect.ValueOf(ctx)})
}

func _delete(uut reflect.Value, ctx context.Context, name string) {
	uut.MethodByName("Delete").Call([]reflect.Value{reflect.ValueOf(ctx), reflect.ValueOf(name)})
}

func startReconciliation(uut reflect.Value, ctx context.Context) {
	uut.MethodByName("StartReconciliation").Call([]reflect.Value{reflect.ValueOf(ctx)})
}

func noGC(uut reflect.Value, ctx context.Context, name string) {
	uut.MethodByName("NoGC").Call([]reflect.Value{reflect.ValueOf(ctx), reflect.ValueOf(name)})
}

func TestController_Add_Success(t *testing.T) {
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			dbm := &db.MockSets{}
			tkr := mockNewTicker()
			defer tkr.restoreNewTicker()
			uut := tc.makeUUT(dbm)

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			run(uut, ctx)

			name := "test"
			fail := func(error) { t.Error("controller called fail func unexpectedly") }
			stat := &statser.MockStatser{}
			add(uut, ctx, name, tc.set, fail, stat)

			startReconciliation(uut, ctx)

			tkr.reconcile(t, ctx)

			g.Eventually(dbm.Calls).Should(ContainElement(
				db.Call{Method: "Put" + tc.name, Name: name, Value: tc.set.Interface()}))
			g.Expect(countMethod(dbm, "Put"+tc.name)()).To(Equal(1))

			dbm.Metas = append(dbm.Metas, db.Meta{Name: name})

			tkr.reconcile(t, ctx)

			g.Consistently(countMethod(dbm, "Put"+tc.name)).
				Should(Equal(1), "should not add a second time")
		})
	}
}

func TestController_Delete_Success(t *testing.T) {
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {

			g := NewWithT(t)
			name := "testdelete"
			dbm := &db.MockSets{Metas: []db.Meta{{Name: name}}}
			tkr := mockNewTicker()
			defer tkr.restoreNewTicker()
			uut := tc.makeUUT(dbm)

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			run(uut, ctx)

			_delete(uut, ctx, name)
			//uut.StartReconciliation(ctx)
			uut.MethodByName("StartReconciliation").Call([]reflect.Value{reflect.ValueOf(ctx)})

			// Test idempotency
			_delete(uut, ctx, name)
			startReconciliation(uut, ctx)

			tkr.reconcile(t, ctx)

			g.Eventually(dbm.Calls).Should(ContainElement(db.Call{Method: "Delete" + tc.name, Name: name}))
			g.Expect(countMethod(dbm, "Delete"+tc.name)()).To(Equal(1))

			dbm.Metas = nil

			tkr.reconcile(t, ctx)

			g.Consistently(countMethod(dbm, "Delete"+tc.name)).
				Should(Equal(1), "should not delete a second time")
		})
	}
}

func TestController_GC_Success(t *testing.T) {
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			dbm := &db.MockSets{}
			tkr := mockNewTicker()
			defer tkr.restoreNewTicker()
			uut := tc.makeUUT(dbm)

			gcName := "shouldGC"
			noGCName := "shouldNotGC"
			var gcSeqNo int64 = 7
			var gcPrimaryTerm int64 = 8
			dbm.Metas = append(dbm.Metas, db.Meta{Name: gcName, SeqNo: &gcSeqNo, PrimaryTerm: &gcPrimaryTerm})

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			run(uut, ctx)
			noGC(uut, ctx, noGCName)
			startReconciliation(uut, ctx)

			tkr.reconcile(t, ctx)

			g.Eventually(dbm.Calls).Should(ContainElement(db.Call{
				Method:      "Delete" + tc.name,
				Name:        gcName,
				SeqNo:       &gcSeqNo,
				PrimaryTerm: &gcPrimaryTerm,
			}))
			g.Expect(countMethod(dbm, "Delete"+tc.name)()).To(Equal(1), "should only GC one set")
		})
	}
}

func TestController_Update_Success(t *testing.T) {
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			name := "test"
			var seqNo int64 = 11
			var primaryTerm int64 = 12
			dbm := &db.MockSets{Metas: []db.Meta{{Name: name, SeqNo: &seqNo, PrimaryTerm: &primaryTerm}}}
			tkr := mockNewTicker()
			defer tkr.restoreNewTicker()
			uut := tc.makeUUT(dbm)

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			run(uut, ctx)

			fail := func(error) { t.Error("controller called fail func unexpectedly") }
			stat := &statser.MockStatser{}
			add(uut, ctx, name, tc.set, fail, stat)

			startReconciliation(uut, ctx)

			tkr.reconcile(t, ctx)

			g.Eventually(dbm.Calls).Should(ContainElement(
				db.Call{Method: "Put" + tc.name, Name: name, Value: tc.set.Interface()}))
			g.Expect(countMethod(dbm, "Put"+tc.name)()).To(Equal(1))

			tkr.reconcile(t, ctx)

			g.Consistently(countMethod(dbm, "Put"+tc.name)).
				Should(Equal(1), "should not update a second time")
		})
	}
}

func TestController_Reconcile_FailToList(t *testing.T) {
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			dbm := &db.MockSets{Error: errors.New("test")}
			tkr := mockNewTicker()
			defer tkr.restoreNewTicker()
			uut := tc.makeUUT(dbm)

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			run(uut, ctx)

			aName := "added"
			var failed bool
			fail := func(error) { failed = true }
			stat := &statser.MockStatser{}
			add(uut, ctx, aName, tc.set, fail, stat)

			gName := "nogc"
			noGC(uut, ctx, gName)

			startReconciliation(uut, ctx)

			tkr.reconcile(t, ctx)

			g.Eventually(func() []v3.ErrorCondition { return stat.Status().ErrorConditions }).Should(HaveLen(1))
			g.Expect(failed).To(BeFalse())
		})
	}
}

func TestController_Add_FailToPut(t *testing.T) {
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			dbm := &db.MockSets{PutError: errors.New("test")}
			tkr := mockNewTicker()
			defer tkr.restoreNewTicker()
			uut := tc.makeUUT(dbm)

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			run(uut, ctx)

			name := "test"
			var failed bool
			fail := func(error) { failed = true }
			stat := &statser.MockStatser{}
			add(uut, ctx, name, tc.set, fail, stat)

			startReconciliation(uut, ctx)

			tkr.reconcile(t, ctx)

			g.Eventually(dbm.Calls).Should(ContainElement(
				db.Call{Method: "Put" + tc.name, Name: name, Value: tc.set.Interface()}))
			g.Expect(countMethod(dbm, "Put"+tc.name)()).To(Equal(1))

			// Potential race condition between call to Put and recording the error, so we just
			// need the error to eventually be recorded.
			g.Eventually(func() int { return len(stat.Status().ErrorConditions) }).Should(Equal(1))
			g.Expect(stat.Status().ErrorConditions[0].Type).To(Equal(statser.ElasticSyncFailed))

			// Potential race condition on calling of the fail function, so we just need it to eventually
			// have been called.
			g.Eventually(failed).Should(BeTrue())

			dbm.PutError = nil
			tkr.reconcile(t, ctx)

			g.Eventually(countMethod(dbm, "Put"+tc.name)).
				Should(Equal(2), "should retry put")
			g.Eventually(func() []v3.ErrorCondition { return stat.Status().ErrorConditions }).Should(HaveLen(0), "should clear error on success")
		})
	}
}

func TestController_GC_NotFound(t *testing.T) {
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			dbm := &db.MockSets{DeleteError: &elastic.Error{Status: http.StatusNotFound}}
			tkr := mockNewTicker()
			defer tkr.restoreNewTicker()
			uut := tc.makeUUT(dbm)

			gcName := "shouldGC"
			var gcSeqNo int64 = 7
			var gcPrimaryTerm int64 = 8
			dbm.Metas = append(dbm.Metas, db.Meta{Name: gcName, SeqNo: &gcSeqNo, PrimaryTerm: &gcPrimaryTerm})

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			run(uut, ctx)
			startReconciliation(uut, ctx)

			tkr.reconcile(t, ctx)

			g.Eventually(dbm.Calls).Should(ContainElement(db.Call{
				Method:      "Delete" + tc.name,
				Name:        gcName,
				SeqNo:       &gcSeqNo,
				PrimaryTerm: &gcPrimaryTerm,
			}))
			g.Expect(countMethod(dbm, "Delete"+tc.name)()).To(Equal(1))

			dbm.Metas = nil
			tkr.reconcile(t, ctx)
			g.Consistently(countMethod(dbm, "Delete"+tc.name)).
				Should(Equal(1), "should not retry delete")
		})
	}
}

func TestController_GC_Error(t *testing.T) {
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			dbm := &db.MockSets{DeleteError: errors.New("test")}
			tkr := mockNewTicker()
			defer tkr.restoreNewTicker()
			uut := tc.makeUUT(dbm)

			gcName := "shouldGC"
			var gcSeqNo int64 = 7
			var gcPrimaryTerm int64 = 8
			dbm.Metas = append(dbm.Metas, db.Meta{Name: gcName, SeqNo: &gcSeqNo, PrimaryTerm: &gcPrimaryTerm})

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			run(uut, ctx)
			startReconciliation(uut, ctx)

			tkr.reconcile(t, ctx)

			g.Eventually(dbm.Calls).Should(ContainElement(db.Call{
				Method:      "Delete" + tc.name,
				Name:        gcName,
				SeqNo:       &gcSeqNo,
				PrimaryTerm: &gcPrimaryTerm,
			}))
			g.Expect(countMethod(dbm, "Delete"+tc.name)()).To(Equal(1))

			dbm.DeleteError = nil
			tkr.reconcile(t, ctx)
			g.Eventually(countMethod(dbm, "Delete"+tc.name)).
				Should(Equal(2), "should retry delete")
		})
	}
}

func TestController_NewTicker(t *testing.T) {
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dbm := &db.MockSets{}
			uut := tc.makeUUT(dbm)

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			run(uut, ctx)
			startReconciliation(uut, ctx)

			// Second call ensures we exercise the "real" ticker code
			startReconciliation(uut, ctx)
		})
	}
}

type mockTicker struct {
	oldTicker func() *time.Ticker
	ticks     chan<- time.Time
}

func mockNewTicker() *mockTicker {
	ticks := make(chan time.Time)
	mt := &mockTicker{oldTicker: controller.NewTicker, ticks: ticks}
	tkr := time.Ticker{C: ticks}
	controller.NewTicker = func() *time.Ticker { return &tkr }
	return mt
}

func (m *mockTicker) restoreNewTicker() {
	controller.NewTicker = m.oldTicker
}

func (m *mockTicker) reconcile(t *testing.T, ctx context.Context) {
	select {
	case <-ctx.Done():
		t.Error("reconcile hangs")
	case m.ticks <- time.Now():
	}
}

func countMethod(client *db.MockSets, method string) func() int {
	return func() int {
		n := 0
		for _, c := range client.Calls() {
			if c.Method == method {
				n++
			}
		}
		return n
	}
}
