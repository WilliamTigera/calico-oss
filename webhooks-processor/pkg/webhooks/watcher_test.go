package webhooks_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/projectcalico/calico/libcalico-go/lib/options"
	calicoWatch "github.com/projectcalico/calico/libcalico-go/lib/watch"
	"github.com/projectcalico/calico/webhooks-processor/pkg/testutils"
	"github.com/projectcalico/calico/webhooks-processor/pkg/webhooks"
	v3 "github.com/tigera/api/pkg/apis/projectcalico/v3"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes/fake"
)

type MockCtrl struct {
	webhooksChan    chan calicoWatch.Event
	receivedUpdates []calicoWatch.Event
}

func NewMockCtrl() *MockCtrl {
	return &MockCtrl{
		webhooksChan:    make(chan calicoWatch.Event),
		receivedUpdates: []calicoWatch.Event{},
	}
}

func (m *MockCtrl) WebhookEventsChan() chan<- calicoWatch.Event {
	return m.webhooksChan
}

func (m *MockCtrl) K8sEventsChan() chan<- watch.Event {
	// This is just to provide the expected interface for the controller.
	// K8sEventsChan() is not being used in the context of this test.
	return make(chan<- watch.Event)
}

func (m *MockCtrl) Run(ctx context.Context, wg *sync.WaitGroup) {
	defer wg.Done()
	for {
		select {
		case update := <-m.webhooksChan:
			fmt.Println(update.Type)
			m.receivedUpdates = append(m.receivedUpdates, update)
		case <-ctx.Done():
			return
		}
	}
}

func TestWebhookWatcherUpdaterMissingDeletions(t *testing.T) {
	// RECAP: this tests verifies the corner case where:
	// - the webhooks watcher/updater performs list and watch operations
	// - during the watch operation it receives some webhooks updates
	// - then the watch operation is then terminated
	// - the next list operation retrieves webhooks list inconsistent with the state of the internal webhooks inventory
	// - therefore we should observe generated DELETE event issued for the inconsistencies
	// Fact that the List() operation of &testutils.FakeSecurityEventWebhook{}
	// always returns an empty list is of webhooks leveraged here.

	// --- BOILERPLATE ---

	mockCtrl := NewMockCtrl()
	mockWebhooksClient := &testutils.FakeSecurityEventWebhook{DontCloseWatchOnCtxCancellation: true}
	watcherUpdater := webhooks.NewWebhookWatcherUpdater().
		WithK8sClient(fake.NewSimpleClientset()).
		WithWebhooksClient(mockWebhooksClient).
		WithController(mockCtrl)

	wg := new(sync.WaitGroup)
	defer wg.Wait()

	ctx, ctxCancel := context.WithTimeout(context.Background(), time.Minute)
	defer ctxCancel()

	wg.Add(2)
	go watcherUpdater.Run(ctx, wg)
	go mockCtrl.Run(ctx, wg)

	// wait for the mockWebhooksClient watch to be ready
	// (Watch() inside watcherUpdater needs to be called before Update() calls are possible)
	for mockWebhooksClient.Watcher == nil {
		<-time.After(100 * time.Millisecond)
	}

	// --- ACTUAL TEST STARTS HERE ---

	webhook := v3.SecurityEventWebhook{
		ObjectMeta: v1.ObjectMeta{Name: "test-webhook"},
	}
	// this update will result in ADDED event type sent to the controller:
	mockWebhooksClient.Update(ctx, &webhook, options.SetOptions{})
	// this update will result in MODIFIED event type sent to the controller:
	mockWebhooksClient.Update(ctx, &webhook, options.SetOptions{})
	// NOTE: we are NOT sending DELETED event type here.

	// let's now close the watcher channel - this should result in reconcilliation and the controller
	// should also receive watch.Deleted event type after the initial List() call detect inconsistencies:
	mockWebhooksClient.Watcher.Stop()

	// wait for things to settle after the above:
	<-time.After(200 * time.Millisecond)

	// make sure the updates received by the controller are the ones sent here (ADDED/MODIFIED)
	// and one after reconcilliation (DELETED):
	if len(mockCtrl.receivedUpdates) != 3 {
		t.Error("unexpected number of updates received")
	}
	if mockCtrl.receivedUpdates[0].Type != calicoWatch.Added {
		t.Error("unexpected received update type (1)")
	}
	if mockCtrl.receivedUpdates[1].Type != calicoWatch.Modified {
		t.Error("unexpected received update type (2)")
	}
	if mockCtrl.receivedUpdates[2].Type != calicoWatch.Deleted {
		t.Error("unexpected received update type (3)")
	}
}
