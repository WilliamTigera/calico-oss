// Copyright 2019 Tigera Inc. All rights reserved.

package globalnetworksets

import (
	"context"
	"errors"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/watch"

	"github.com/tigera/intrusion-detection/controller/pkg/calico"
	"github.com/tigera/intrusion-detection/controller/pkg/db"
	"github.com/tigera/intrusion-detection/controller/pkg/feeds/statser"
	"github.com/tigera/intrusion-detection/controller/pkg/util"
)

func TestNewController(t *testing.T) {
	g := NewWithT(t)

	client := &calico.MockGlobalNetworkSetInterface{}
	uut := NewController(client)
	g.Expect(uut).ToNot(BeNil())
}

func TestController_Add_Success(t *testing.T) {
	g := NewWithT(t)

	client := &calico.MockGlobalNetworkSetInterface{W: &calico.MockWatch{C: make(chan watch.Event)}}
	uut := NewController(client)

	// Grab a ref to the workqueue, which we'll use to measure progress.
	q := uut.(*controller).queue

	gns := util.NewGlobalNetworkSet("test")
	fail := func() { t.Error("controller called fail func unexpectedly") }
	stat := &statser.MockStatser{}
	// Set an error which we expect to clear.
	stat.Error(statser.GlobalNetworkSetSyncFailed, errors.New("test"))
	uut.Add(gns, fail, stat)
	g.Expect(q.Len()).Should(Equal(1))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	uut.Run(ctx)

	ex := gns.DeepCopy()
	// all created sets are labelled.
	ex.Labels = map[string]string{LabelKey: LabelValue}

	// Wait for queue to be processed
	g.Eventually(q.Len).Should(Equal(0))
	g.Expect(client.Calls()).To(ContainElement(db.Call{Method: "Create", GNS: ex}))
	g.Expect(stat.Status().ErrorConditions).To(HaveLen(0))

	// The watch will send the GNS back to the informer
	client.W.C <- watch.Event{
		Type:   watch.Added,
		Object: client.GlobalNetworkSet,
	}

	// Expect not to create or update, since the GNS is identical
	g.Consistently(countMethod(client, "Create")).Should(Equal(1))
}

func TestController_Delete(t *testing.T) {
	g := NewWithT(t)

	gns := util.NewGlobalNetworkSet("test")
	gns.Labels = map[string]string{LabelKey: LabelValue}
	client := &calico.MockGlobalNetworkSetInterface{GlobalNetworkSet: gns}
	uut := NewController(client)

	// Grab a ref to the workqueue, which we'll use to measure progress.
	q := uut.(*controller).queue

	uut.NoGC(gns)
	g.Expect(q.Len()).To(Equal(0))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	uut.Run(ctx)

	// Don't GC
	g.Consistently(countMethod(client, "Delete")).Should(Equal(0))

	// Ensure all processing is done before triggering the delete, otherwise we
	// can sometimes get two calls to delete.
	g.Eventually(q.Len).Should(Equal(0))

	uut.Delete(gns)
	g.Eventually(countMethod(client, "Delete")).Should(Equal(1))
	g.Expect(client.Calls()).To(ContainElement(db.Call{Method: "Delete", Name: gns.Name}))
}

func TestController_Update(t *testing.T) {
	g := NewWithT(t)

	gns := util.NewGlobalNetworkSet("test")
	gns.Labels = map[string]string{LabelKey: LabelValue}
	gns.ResourceVersion = "test_version"
	client := &calico.MockGlobalNetworkSetInterface{GlobalNetworkSet: gns}
	uut := NewController(client)

	// Grab a ref to the workqueue, which we'll use to measure progress.
	q := uut.(*controller).queue

	fail := func() { t.Error("controller called fail func unexpectedly") }
	stat := &statser.MockStatser{}
	uut.Add(gns, fail, stat)
	g.Expect(q.Len()).Should(Equal(1))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	uut.Run(ctx)

	// Wait for queue to be processed
	g.Eventually(q.Len).Should(Equal(0))

	// Add the GNS with different data
	gns1 := gns.DeepCopy()
	gns1.Spec.Nets = []string{"192.168.9.45"}
	gns1e := gns1.DeepCopy()
	// added GNS doesn't know the ResourceVersion
	gns1.ResourceVersion = ""
	uut.Add(gns1, fail, stat)

	g.Eventually(countMethod(client, "Update")).Should(Equal(1))
	g.Expect(client.Calls()).To(ContainElement(db.Call{Method: "Update", GNS: gns1e}))

	// Update labels
	gns2 := gns1e.DeepCopy()
	gns2.Labels["mock"] = "yes"
	gns2e := gns2.DeepCopy()
	// added GNS doesn't know the resource version
	gns2.ResourceVersion = ""
	uut.Add(gns2, fail, stat)

	g.Eventually(countMethod(client, "Update")).Should(Equal(2))
	g.Expect(client.Calls()).To(ContainElement(db.Call{Method: "Update", GNS: gns2e}))
}

// Add and then delete a GNS before there is a chance to process it.
func TestController_AddDelete(t *testing.T) {
	g := NewWithT(t)

	gns := util.NewGlobalNetworkSet("test")
	client := &calico.MockGlobalNetworkSetInterface{}
	uut := NewController(client)

	// Grab a ref to the workqueue, which we'll use to measure progress.
	q := uut.(*controller).queue

	fail := func() { t.Error("controller called fail func unexpectedly") }
	stat := &statser.MockStatser{}
	uut.Add(gns, fail, stat)
	g.Expect(q.Len()).Should(Equal(1))
	uut.Delete(gns)
	g.Expect(q.Len()).Should(Equal(1), "More more on same key should not add to workqueue")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	uut.Run(ctx)

	// Wait for queue to be processed
	g.Eventually(q.Len).Should(Equal(0))

	g.Expect(client.Calls()).To(HaveLen(0))
}

func TestController_AddRetry(t *testing.T) {
	g := NewWithT(t)

	gns := util.NewGlobalNetworkSet("test")
	client := &calico.MockGlobalNetworkSetInterface{CreateError: []error{errors.New("test")}}
	uut := NewController(client)

	// Grab a ref to the workqueue, which we'll use to measure progress.
	q := uut.(*controller).queue

	fail := func() { t.Error("controller called fail func unexpectedly") }
	stat := &statser.MockStatser{}
	uut.Add(gns, fail, stat)
	g.Expect(q.Len()).Should(Equal(1))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	uut.Run(ctx)

	// Should be retried.
	g.Eventually(countMethod(client, "Create")).Should(Equal(2))
}

func TestController_AddFail(t *testing.T) {
	g := NewWithT(t)

	gns := util.NewGlobalNetworkSet("test")
	//
	client := &calico.MockGlobalNetworkSetInterface{}
	for i := 0; i < DefaultClientRetries+1; i++ {
		client.CreateError = append(client.CreateError, errors.New("test"))
	}
	uut := NewController(client)

	// Grab a ref to the workqueue, which we'll use to measure progress.
	q := uut.(*controller).queue

	var failed bool
	fail := func() { failed = true }
	stat := &statser.MockStatser{}
	uut.Add(gns, fail, stat)
	g.Expect(q.Len()).Should(Equal(1))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	uut.Run(ctx)

	// Should be retried.
	g.Eventually(countMethod(client, "Create")).Should(Equal(DefaultClientRetries + 1))
	g.Expect(failed).To(BeTrue())
	g.Expect(stat.Status().ErrorConditions).To(HaveLen(1))
	g.Expect(stat.Status().ErrorConditions[0].Type).To(Equal(statser.GlobalNetworkSetSyncFailed))
}

func TestController_ResourceEventHandlerFuncs(t *testing.T) {
	g := NewWithT(t)

	client := &calico.MockGlobalNetworkSetInterface{W: &calico.MockWatch{C: make(chan watch.Event)}}
	uut := NewController(client)

	// Grab a ref to the workqueue, which we'll use to measure progress.
	q := uut.(*controller).queue

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	uut.Run(ctx)
	g.Expect(q.Len()).To(Equal(0))

	gns := util.NewGlobalNetworkSet("test")
	client.W.C <- watch.Event{
		Type:   watch.Added,
		Object: gns,
	}

	gnsUp := gns.DeepCopy()
	gnsUp.Spec.Nets = []string{"10.1.10.1"}
	client.W.C <- watch.Event{
		Type:   watch.Modified,
		Object: gns,
	}

	gnsDel := gnsUp.DeepCopy()
	client.W.C <- watch.Event{
		Type:   watch.Deleted,
		Object: gnsDel,
	}

	g.Eventually(q.Len).Should(Equal(0))
}

// Test the code that handles failing to sync. Very little to assert, but making
// sure it doesn't panic or lock.
func TestController_FailToSync(t *testing.T) {
	g := NewWithT(t)

	client := &calico.MockGlobalNetworkSetInterface{Error: errors.New("test")}
	uut := NewController(client)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	informer := uut.(*controller).informer

	uut.Run(ctx)
	g.Consistently(informer.HasSynced).Should(BeFalse())
	cancel()
	g.Consistently(informer.HasSynced).Should(BeFalse())
}

// Test the code that handles failing to sync. Very little to assert, but making
// sure it doesn't panic or lock.
func TestController_ShutDown(t *testing.T) {
	g := NewWithT(t)

	client := &calico.MockGlobalNetworkSetInterface{}
	uut := NewController(client)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	informer := uut.(*controller).informer
	q := uut.(*controller).queue

	uut.Run(ctx)
	g.Eventually(informer.HasSynced).Should(BeTrue())
	cancel()

	g.Eventually(q.ShuttingDown).Should(BeTrue())
	g.Eventually(q.Len).Should(Equal(0))
}

func TestController_DeleteFailure(t *testing.T) {
	g := NewWithT(t)

	gns := util.NewGlobalNetworkSet("test")
	client := &calico.MockGlobalNetworkSetInterface{
		GlobalNetworkSet: gns,
		DeleteError:      errors.New("test"),
	}
	uut := NewController(client)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	uut.Run(ctx)

	g.Eventually(countMethod(client, "Delete")).Should(Equal(DefaultClientRetries + 1))
}

func TestController_UpdateFailure(t *testing.T) {
	g := NewWithT(t)

	gns := util.NewGlobalNetworkSet("test")
	client := &calico.MockGlobalNetworkSetInterface{
		GlobalNetworkSet: gns,
		UpdateError:      errors.New("test"),
	}
	uut := NewController(client)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	gnsUp := gns.DeepCopy()
	gnsUp.Spec.Nets = []string{"4.5.6.7"}
	fail := func() {}
	stat := &statser.MockStatser{}
	uut.Add(gnsUp, fail, stat)

	uut.Run(ctx)

	g.Eventually(countMethod(client, "Update")).Should(Equal(DefaultClientRetries + 1))
}

func countMethod(client *calico.MockGlobalNetworkSetInterface, method string) func() int {
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
