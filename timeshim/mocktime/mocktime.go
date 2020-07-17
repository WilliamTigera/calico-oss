// Copyright (c) 2020 Tigera, Inc. All rights reserved.

package mocktime

import (
	"sort"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/projectcalico/felix/timeshim"
)

var startTime, _ = time.Parse(time.RFC3339, "2006-01-02T15:04:05Z")

func New() *MockTime {
	return &MockTime{
		currentTime: startTime,
	}
}

var _ timeshim.Interface = (*MockTime)(nil)

type MockTime struct {
	lock sync.Mutex

	currentTime   time.Time
	autoIncrement time.Duration
	timers        []mockTimer
}

type mockTimer struct {
	TimeToFire time.Time
	C          chan time.Time
}

func (m *MockTime) Until(t time.Time) time.Duration {
	return t.Sub(m.Now())
}

func (m *MockTime) After(t time.Duration) <-chan time.Time {
	m.lock.Lock()
	defer m.lock.Unlock()

	c := make(chan time.Time, 1) // Capacity 1 so we don't block on firing

	m.timers = append(m.timers, mockTimer{
		TimeToFire: m.currentTime.Add(t),
		C:          c,
	})

	return c
}

func (m *MockTime) Now() time.Time {
	m.lock.Lock()
	defer m.lock.Unlock()

	t := m.currentTime
	m.incrementTimeLockHeld(m.autoIncrement)
	return t
}

func (m *MockTime) Since(t time.Time) time.Duration {
	return m.Now().Sub(t)
}

func (m *MockTime) SetAutoIncrement(t time.Duration) {
	m.autoIncrement = t
}

func (m *MockTime) IncrementTime(t time.Duration) {
	m.lock.Lock()
	defer m.lock.Unlock()

	m.incrementTimeLockHeld(t)
}

func (m *MockTime) HasTimers() bool {
	m.lock.Lock()
	defer m.lock.Unlock()

	return len(m.timers) > 0
}

func (m *MockTime) incrementTimeLockHeld(t time.Duration) {
	if t == 0 {
		return
	}

	m.currentTime = m.currentTime.Add(t)
	logrus.WithField("increment", t).WithField("t", m.currentTime.Sub(startTime)).Info("Incrementing time")

	if len(m.timers) == 0 {
		return
	}

	sort.Slice(m.timers, func(i, j int) bool {
		return m.timers[i].TimeToFire.Before(m.timers[j].TimeToFire)
	})

	logrus.WithField("delay", m.timers[0].TimeToFire.Sub(m.currentTime)).Info("Next timer.")

	for len(m.timers) > 0 &&
		(m.timers[0].TimeToFire.Before(m.currentTime) ||
			m.timers[0].TimeToFire.Equal(m.currentTime)) {
		logrus.WithField("timer", m.timers[0]).Info("Firing timer.")
		select {
		case m.timers[0].C <- m.timers[0].TimeToFire: // Should never block since there channel has cap 1.
		default:
			logrus.Panic("Blocked while trying to fire timer")
		}
		m.timers = m.timers[1:]
	}
}
