package worker

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	v3 "github.com/tigera/api/pkg/apis/projectcalico/v3"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
)

type fakeReconciler struct{}

func (fr *fakeReconciler) Reconcile(name types.NamespacedName) error {
	if name.Name != "" {
		return nil
	}
	return fmt.Errorf("error Occurred")
}

func (fr *fakeReconciler) Close() {}

var _ = Describe("Abstract Worker Tests", func() {

	var (
		rateLimitInterface = workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter())
		reconciler         = fakeReconciler{}

		newWorker = worker{
			RateLimitingInterface: rateLimitInterface,
			reconciler:            &reconciler,
			maxRequeueAttempts:    DefaultMaxRequeueAttempts,
		}
	)

	Context("Test Worker worker Queue", func() {
		It("Worker health check ", func() {
			ctx, _ := context.WithCancel(context.Background())
			ponger := newWorker.AddWatch(&cache.ListWatch{}, &v3.GlobalAlert{})

			go newWorker.startWorker()

			for _, w := range newWorker.watches {
				go newWorker.listenForPings(w.ponger, ctx.Done())

			}

			Expect(ponger.Ping(ctx)).To(BeNil())
		})
	})
})
