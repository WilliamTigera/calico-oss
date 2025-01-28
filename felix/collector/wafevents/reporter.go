package wafevents

import (
	cryptorand "crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/projectcalico/calico/felix/collector/types"
	"github.com/projectcalico/calico/felix/proto"
	"github.com/projectcalico/calico/libcalico-go/lib/health"
	v1 "github.com/projectcalico/calico/linseed/pkg/apis/v1"
)

type WAFEventReporter struct {
	dispatchers      []types.Reporter
	flushTrigger     <-chan time.Time
	healthAggregator *health.HealthAggregator
	running          bool
	mu               sync.Mutex

	buf *buffer
}

const (
	wafEventHealthName     = "WAFEventReporter"
	wafEventHealthInterval = 10 * time.Second
)

type buffer struct {
	buf map[aggregationKey]*Report
	mu  sync.Mutex

	hashDelimiter,
	hashSalt []byte
}

type aggregationKey [sha256.Size]byte

type Report struct {
	*proto.WAFEvent
	Src, Dst *v1.WAFEndpoint

	count int
}

func newBuffer() *buffer {
	hashSalt := make([]byte, sha256.Size)
	_, err := cryptorand.Read(hashSalt)
	if err != nil {
		log.WithError(err).Fatal("could not create hash salt")
	}

	return &buffer{
		buf:           map[aggregationKey]*Report{},
		hashDelimiter: []byte{'\n'},
		hashSalt:      hashSalt,
	}
}

func NewReporter(dispatchers []types.Reporter, flushInterval time.Duration, healthAggregator *health.HealthAggregator) *WAFEventReporter {
	flushTrigger := time.NewTicker(flushInterval)
	return NewReporterWithShims(dispatchers, flushTrigger.C, healthAggregator)
}

func NewReporterWithShims(dispatchers []types.Reporter, flushTrigger <-chan time.Time, healthAggregator *health.HealthAggregator) *WAFEventReporter {
	if len(dispatchers) == 0 {
		log.Panic("dispatchers argument can not be empty")
	}
	if healthAggregator != nil {
		healthAggregator.RegisterReporter(wafEventHealthName, &health.HealthReport{Live: true, Ready: true}, wafEventHealthInterval*2)
	}
	return &WAFEventReporter{
		dispatchers:      dispatchers,
		flushTrigger:     flushTrigger,
		healthAggregator: healthAggregator,
		buf:              newBuffer(),
	}
}

func (r *WAFEventReporter) Start() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.running {
		r.running = true
		go r.run()
	}
	return nil
}

func (r *WAFEventReporter) Report(event interface{}) error {
	switch e := event.(type) {
	case *Report:
		log.Debugf("Reporting buffer %+v", e)
		return r.reportToBuffer(e)
	default:
		return fmt.Errorf("Unknown event type: %T", e)
	}
}

func (r *WAFEventReporter) run() {
	healthTicks := time.NewTicker(wafEventHealthInterval)
	defer healthTicks.Stop()

	for {
		select {
		case <-r.flushTrigger:
			r.flush()
		case <-healthTicks.C:
			r.reportHealth()
		}
	}
}

func (r *WAFEventReporter) reportToBuffer(report *Report) error {
	// Asserts
	if report == nil {
		return errors.New("event argument can't be nil")
	} else if report.Request == nil {
		return errors.New("event.Request can't be nil")
	}

	r.buf.add(report)
	return nil
}

func (r *WAFEventReporter) flush() {
	buf := r.buf.cpyClearBuffer()
	updates := buf.getUpdates()

	log.WithField("updates", updates).Debugf("Flushing WAFEvents")
	for _, d := range r.dispatchers {
		if err := d.Report(updates); err != nil {
			log.WithError(err).WithFields(log.Fields{
				"dispatcher": d,
				"updates":    updates,
			}).Error("Error trying to flush WAFEvents")
		}
	}
}

func (r *WAFEventReporter) reportHealth() {
	if r.healthAggregator != nil {
		r.healthAggregator.Report(wafEventHealthName, &health.HealthReport{
			Live:  true,
			Ready: r.canPublish(),
		})
	}
}

func (r *WAFEventReporter) canPublish() bool {
	for _, d := range r.dispatchers {
		err := d.Start()
		if err != nil {
			log.WithError(err).Error("dispatcher unable to initialize")
			return false
		}
	}
	return true
}

func (b *buffer) add(report *Report) {
	key, err := b.hashKey(report)
	if err != nil {
		log.WithError(err).WithField("report", report).Error("failed to add report to buffer")
		return
	}

	b.mu.Lock()
	idxReport := b.buf[key]
	if idxReport == nil {
		b.buf[key] = report
		idxReport = report
	}
	b.mu.Unlock()

	idxReport.count++
}

func (b *buffer) cpyClearBuffer() *buffer {
	b.mu.Lock()
	defer b.mu.Unlock()

	cpy := &buffer{buf: b.buf}
	b.buf = map[aggregationKey]*Report{}
	return cpy
}

func (b *buffer) getUpdates() (updates []*v1.WAFLog) {

	for _, r := range b.buf {
		// XXX we need to imporove on linseed to inform how many
		// requests were aggregated adding the r.count to the report
		update := &v1.WAFLog{
			RequestId:   r.TxId,
			Source:      r.Src,
			Destination: r.Dst,
			Msg:         fmt.Sprintf("WAF detected %d violations [ %s ]", len(r.Rules), r.Action),
			Path:        r.Request.Path,
			Method:      r.Request.Method,
			Protocol:    fmt.Sprintf("HTTP/%s", r.Request.Version),
			Host:        r.Host,
			Timestamp:   time.Unix(r.Timestamp.Seconds, int64(r.Timestamp.Nanos)).UTC(),
		}
		for _, rule := range r.Rules {
			update.Rules = append(update.Rules, v1.WAFRuleHit{
				Message:    rule.Rule.Message,
				Disruptive: rule.Disruptive,
				Id:         rule.Rule.Id,
				Severity:   rule.Rule.Severity,
				File:       rule.Rule.File,
				Line:       rule.Rule.Line,
			})
		}
		updates = append(updates, update)
	}
	return updates
}

func (b *buffer) hashKey(report *Report) (key aggregationKey, err error) {
	digest := sha256.New()
	_, err = digest.Write([]byte(report.Request.Method))
	if err != nil {
		return
	}
	_, err = digest.Write(b.hashDelimiter)
	if err != nil {
		return
	}
	_, err = digest.Write([]byte(report.Host))
	if err != nil {
		return
	}
	_, err = digest.Write(b.hashDelimiter)
	if err != nil {
		return
	}
	_, err = digest.Write([]byte(report.Request.Path))
	if err != nil {
		return
	}
	_, err = digest.Write(b.hashDelimiter)
	if err != nil {
		return
	}
	_, err = digest.Write([]byte(report.Action))
	if err != nil {
		return
	}
	_, err = digest.Write(b.hashDelimiter)
	if err != nil {
		return
	}
	for _, rule := range report.Rules {
		_, err = digest.Write([]byte(rule.Rule.Id))
		if err != nil {
			return
		}
		_, err = digest.Write(b.hashDelimiter)
		if err != nil {
			return
		}
	}
	_, err = digest.Write(b.hashSalt)
	if err != nil {
		return
	}
	copy(key[:], digest.Sum(nil))
	return
}
