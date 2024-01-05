// Copyright (c) 2023 Tigera, Inc. All rights reserved.

package webhooks

import (
	"context"
	"sync"
	"time"

	api "github.com/tigera/api/pkg/apis/projectcalico/v3"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	toolsWatch "k8s.io/client-go/tools/watch"

	"github.com/sirupsen/logrus"

	"github.com/projectcalico/calico/libcalico-go/lib/clientv3"
	"github.com/projectcalico/calico/libcalico-go/lib/options"
)

type WebhookWatcherUpdater struct {
	client             *kubernetes.Clientset
	whClient           clientv3.SecurityEventWebhookInterface
	controller         WebhookControllerInterface
	webhookUpdatesChan chan *api.SecurityEventWebhook
}

func NewWebhookWatcherUpdater() (watcher *WebhookWatcherUpdater) {
	watcher = new(WebhookWatcherUpdater)
	watcher.webhookUpdatesChan = make(chan *api.SecurityEventWebhook)
	return
}

func (w *WebhookWatcherUpdater) WithWebhooksClient(client clientv3.SecurityEventWebhookInterface) *WebhookWatcherUpdater {
	w.whClient = client
	return w
}

func (w *WebhookWatcherUpdater) WithK8sClient(client *kubernetes.Clientset) *WebhookWatcherUpdater {
	w.client = client
	return w
}

func (w *WebhookWatcherUpdater) WithController(controller WebhookControllerInterface) *WebhookWatcherUpdater {
	w.controller = controller
	return w
}

func (w *WebhookWatcherUpdater) UpdatesChan() chan<- *api.SecurityEventWebhook {
	return w.webhookUpdatesChan
}

func (w *WebhookWatcherUpdater) Run(ctx context.Context, ctxCancel context.CancelFunc, wg *sync.WaitGroup) {
	defer ctxCancel()
	defer wg.Done()
	defer logrus.Info("Webhook watcher is terminating")

	logrus.Info("Watching for webhook definitions")

	// watch for webhook updates to apply:
	go func() {
		for {
			select {
			case webhook := <-w.webhookUpdatesChan:
				logEntry(webhook).Debug("Updating webhook")
				if _, err := w.whClient.Update(ctx, webhook, options.SetOptions{}); err != nil {
					logrus.WithError(err).Warn("Unable to update SecurityEventWebhook definition")
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	// watch for config map and secret updates:
	cmWatcher, err := toolsWatch.NewRetryWatcher("1", &cache.ListWatch{
		WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
			return w.client.CoreV1().ConfigMaps(ConfigVarNamespace).Watch(ctx, metav1.ListOptions{})
		},
	})
	if err != nil {
		logrus.WithError(err).Error("Unable to watch ConfigMap resources")
		return
	}
	secretWatcher, err := toolsWatch.NewRetryWatcher("1", &cache.ListWatch{
		WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
			return w.client.CoreV1().Secrets(ConfigVarNamespace).Watch(ctx, metav1.ListOptions{})
		},
	})
	if err != nil {
		logrus.WithError(err).Error("Unable to watch Secret resources")
		return
	}
	go func() {
		defer cmWatcher.Stop()
		defer secretWatcher.Stop()
		for ctx.Err() == nil {
			select {
			case event := <-cmWatcher.ResultChan():
				w.controller.K8sEventsChan() <- event
			case event := <-secretWatcher.ResultChan():
				w.controller.K8sEventsChan() <- event
			}
		}
	}()

	// allow some time to pass for the above to process existing cms
	// and secrets before processing existing webhooks on start
	time.Sleep(5 * time.Second)

	// watch for webhook updates to process:
	for ctx.Err() == nil {
		watcher, err := w.whClient.Watch(ctx, options.ListOptions{})
		if err != nil {
			logrus.WithError(err).Error("Unable to watch for SecurityEventWebhook resources")
			return
		}
		defer watcher.Stop()
		for event := range watcher.ResultChan() {
			w.controller.WebhookEventsChan() <- event
		}
	}
}
