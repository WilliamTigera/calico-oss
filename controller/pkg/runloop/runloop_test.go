package runloop

import (
	"context"
	"sync"
	"testing"
	"time"

	. "github.com/onsi/gomega"
)

func TestRunLoop(t *testing.T) {
	g := NewGomegaWithT(t)

	maxDuration := time.Millisecond * 10
	period := 100 * time.Microsecond

	ctx, cancel := context.WithTimeout(context.TODO(), maxDuration)
	defer cancel()

	c := 0
	var err error
	wg := sync.WaitGroup{}
	wg.Add(1)

	cond := sync.Cond{
		L: &sync.Mutex{},
	}

	go func() {
		defer wg.Done()
		err = RunLoop(ctx, func() {
			cond.L.Lock()
			cond.Signal()
			cond.L.Unlock()
			c++
		}, period)
	}()

	// Measure the difference in time between executions
	cond.L.Lock()
	cond.Wait()
	t0 := time.Now()
	cond.L.Unlock()

	cond.L.Lock()
	cond.Wait()
	t1 := time.Now()
	cond.L.Unlock()

	g.Expect(t1.Sub(t0)).Should(BeNumerically(">=", period))

	wg.Wait()
	g.Expect(err).Should(Equal(context.DeadlineExceeded))

	g.Expect(c).Should(BeNumerically("~", maxDuration/period, 1))
}

func TestRunLoopWithReschedule(t *testing.T) {
	g := NewGomegaWithT(t)

	maxDuration := time.Millisecond * 10
	period := 100 * time.Microsecond
	reschedulePeriod := 10 * time.Microsecond

	ctx, cancel := context.WithTimeout(context.TODO(), maxDuration)
	defer cancel()

	run, reschedule := RunLoopWithReschedule()
	g.Expect(reschedule()).Should(HaveOccurred(), "Reschedule function should return an error if RunLoop has not started yet")

	c := 0
	rc := 0
	var res error
	wg := sync.WaitGroup{}
	wg.Add(1)

	cond := sync.Cond{
		L: &sync.Mutex{},
	}

	go func() {
		defer wg.Done()
		res = run(ctx, func() {
			cond.L.Lock()
			cond.Signal()
			cond.L.Unlock()
			c++
		}, period, func() { rc++ }, reschedulePeriod)
	}()

	// Measure the difference in time between executions when reschedule() is called
	cond.L.Lock()
	cond.Wait()
	t0 := time.Now()
	g.Expect(reschedule()).ShouldNot(HaveOccurred(), "Reschedule runs successfully")
	// This must not cause rescheduleFunc to be called again. Tested at the bottom where we check that rc=2
	g.Expect(reschedule()).ShouldNot(HaveOccurred(), "Reschedule runs successfully")
	cond.L.Unlock()

	cond.L.Lock()
	cond.Wait()
	t1 := time.Now()
	// Call reschedule again now that the reschedule has been cleared
	g.Expect(reschedule()).ShouldNot(HaveOccurred(), "Reschedule runs successfully")
	cond.L.Unlock()

	g.Expect(t1.Sub(t0)).Should(BeNumerically("<", period))

	wg.Wait()
	g.Expect(res).Should(Equal(context.DeadlineExceeded))
	g.Expect(c).Should(BeNumerically("~", maxDuration/period, 1+rc))

	g.Expect(reschedule()).Should(HaveOccurred(), "Reschedule function returns an error after the RunLoop terminates and does not panic")
	g.Expect(rc).Should(Equal(2))
}

func TestRunLoopWithRescheduleLongRunningFunction(t *testing.T) {
	g := NewGomegaWithT(t)

	maxDuration := time.Millisecond * 10
	period := 100 * time.Microsecond
	reschedulePeriod := 10 * time.Microsecond
	sleep := time.Millisecond

	ctx, cancel := context.WithTimeout(context.TODO(), maxDuration)
	defer cancel()

	run, reschedule := RunLoopWithReschedule()
	g.Expect(reschedule()).Should(HaveOccurred(), "Reschedule function should return an error if RunLoop has not started yet")

	c := 0
	rc := 0
	var res error
	wg := sync.WaitGroup{}
	wg.Add(1)

	cond := sync.Cond{
		L: &sync.Mutex{},
	}

	go func() {
		defer wg.Done()
		res = run(ctx, func() {
			cond.L.Lock()
			cond.Signal()
			cond.L.Unlock()
			c++
			select {
			case <-ctx.Done():
			case <-time.After(sleep):
			}
		}, period, func() { rc++ }, reschedulePeriod)
	}()

	// Make sure that we can reschedule once while the long-running function is executing, but not twice
	cond.L.Lock()
	cond.Wait()
	g.Expect(reschedule()).ShouldNot(HaveOccurred(), "Reschedule succeeds")
	cond.L.Unlock()

	cond.L.Lock()
	cond.Wait()
	g.Expect(reschedule()).ShouldNot(HaveOccurred(), "Reschedule succeeds")
	cond.L.Unlock()

	wg.Wait()
	g.Expect(res).Should(Equal(context.DeadlineExceeded))

	g.Expect(c).Should(BeNumerically("~", maxDuration/sleep, 1+rc))
}
