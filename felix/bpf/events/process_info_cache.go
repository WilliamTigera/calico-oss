// Copyright (c) 2020-2023 Tigera, Inc. All rights reserved.

package events

import (
	"bytes"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/projectcalico/calico/felix/collector"
	"github.com/projectcalico/calico/felix/collector/types"
	"github.com/projectcalico/calico/felix/collector/types/tuple"
	"github.com/projectcalico/calico/felix/jitter"
)

type ProcessEntry struct {
	collector.ProcessInfo
	expiresAt time.Time
}

// BPFProcessInfoCache reads process information from Linux via kprobes.
type BPFProcessInfoCache struct {
	// Read-Write mutex for process info
	lock sync.RWMutex
	// Map of tuple to process information
	cache map[tuple.Tuple]ProcessEntry

	// Ticker for running the GC thread that reaps expired entries.
	expireTicker jitter.TickerInterface
	// Max time for which an entry is retained.
	entryTTL time.Duration

	stopOnce          sync.Once
	wg                sync.WaitGroup
	stopC             chan struct{}
	eventProcessInfo  <-chan EventProtoStats
	eventTcpStatsInfo <-chan EventTcpStats
	processPathCache  *BPFProcessPathCache
}

// NewBPFProcessInfoCache returns a new BPFProcessInfoCache
func NewBPFProcessInfoCache(eventProcessInfoChan <-chan EventProtoStats, eventTcpStatsInfoChan <-chan EventTcpStats,
	gcInterval time.Duration, entryTTL time.Duration, processPathCache *BPFProcessPathCache) *BPFProcessInfoCache {
	return &BPFProcessInfoCache{
		stopC:             make(chan struct{}),
		eventProcessInfo:  eventProcessInfoChan,
		eventTcpStatsInfo: eventTcpStatsInfoChan,
		expireTicker:      jitter.NewTicker(gcInterval, gcInterval/10),
		entryTTL:          entryTTL,
		cache:             make(map[tuple.Tuple]ProcessEntry),
		lock:              sync.RWMutex{},
		processPathCache:  processPathCache,
	}
}

func (r *BPFProcessInfoCache) Start() error {
	if r.processPathCache != nil {
		r.processPathCache.Start()
	}
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		if r.eventProcessInfo != nil || r.eventTcpStatsInfo != nil {
			r.run()
		}
	}()

	return nil
}

func (r *BPFProcessInfoCache) run() {
	defer r.expireTicker.Stop()
	for {
		select {
		case <-r.stopC:
			return
		case processEvent, ok := <-r.eventProcessInfo:
			if ok {
				info := convertProtoEventToProcessInfo(processEvent)
				log.Debugf("Converted event %+v to process info %+v", processEvent, info)
				r.updateCacheWithProcessInfo(info)
			}
		case tcpStatsEvent, ok := <-r.eventTcpStatsInfo:
			if ok {
				info := convertTcpStatsEventToProcessInfo(tcpStatsEvent)
				log.Debugf("Converted event %+v to process info %+v", tcpStatsEvent, info)
				r.updateCacheWithStats(info)
			}
		case <-r.expireTicker.Channel():
			r.expireCacheEntries()
		}
	}
}

func (r *BPFProcessInfoCache) Stop() {
	if r.processPathCache != nil {
		r.processPathCache.Stop()
	}
	r.stopOnce.Do(func() {
		close(r.stopC)
	})
	r.wg.Wait()
}

func (r *BPFProcessInfoCache) Lookup(tuple tuple.Tuple, direction types.TrafficDirection) (collector.ProcessInfo, bool) {
	r.lock.RLock()
	defer r.lock.RUnlock()

	t := tuple
	if direction == types.TrafficDirInbound {
		// Inbound data is stored in the reverse order.
		t = t.Reverse()
	}
	log.Debugf("Looking up process info for tuple %v in direction %v", tuple, direction)
	if entry, ok := r.cache[t]; ok {
		log.Debugf("Found process info %+v for tuple %+v in direction %v", entry.ProcessInfo, tuple, direction)
		return entry.ProcessInfo, true
	}
	log.Debugf("Process info not found for tuple %+v in direction %v", tuple, direction)
	return collector.ProcessInfo{}, false
}

func (r *BPFProcessInfoCache) Update(tuple tuple.Tuple, dirty bool) {
	r.updateCacheWithTcpStatsDirty(tuple, dirty)
}
func (r *BPFProcessInfoCache) updateCacheWithTcpStatsDirty(tuple tuple.Tuple, dirty bool) {
	r.lock.Lock()
	defer r.lock.Unlock()
	log.Debugf("Setting the dirty flag for TCPStats to %+v", dirty)
	entry, ok := r.cache[tuple]
	if ok {
		entry.TcpStatsData.IsDirty = dirty
		r.cache[tuple] = entry
	}
	// May be entry has expired
}

func (r *BPFProcessInfoCache) updateCacheWithProcessInfo(info collector.ProcessInfo) {
	r.lock.Lock()
	defer r.lock.Unlock()
	log.Debugf("Updating process info %+v", info)
	t := info.Tuple
	if r.processPathCache != nil {
		pathInfo, ok := r.processPathCache.Lookup(info.ProcessData.Pid)
		if ok {
			info.ProcessData.Name = pathInfo.Path
			info.ProcessData.Arguments = pathInfo.Args
		}
	}
	entry, ok := r.cache[t]
	if ok {
		entry.ProcessData = info.ProcessData
		entry.expiresAt = time.Now().Add(r.entryTTL)
		log.Debugf("Process Info cache updated with process data %+v", entry)
		r.cache[info.Tuple] = entry
	} else {
		entry := ProcessEntry{
			ProcessInfo: info,
			expiresAt:   time.Now().Add(r.entryTTL),
		}
		r.cache[info.Tuple] = entry
	}
}

func (r *BPFProcessInfoCache) updateCacheWithStats(info collector.ProcessInfo) {
	r.lock.Lock()
	defer r.lock.Unlock()
	log.Debugf("Updating process info with stats %+v", info)
	t := info.Tuple
	entry, ok := r.cache[t]
	if ok {
		entry.TcpStatsData = info.TcpStatsData
		entry.expiresAt = time.Now().Add(r.entryTTL)
		log.Debugf("Process Info cache updated with TCP stats data %+v", entry)
		r.cache[info.Tuple] = entry
	} else {
		entry := ProcessEntry{
			ProcessInfo: info,
			expiresAt:   time.Now().Add(r.entryTTL),
		}
		r.cache[info.Tuple] = entry
	}
}

func (r *BPFProcessInfoCache) expireCacheEntries() {
	r.lock.Lock()
	defer r.lock.Unlock()

	for tuple, entry := range r.cache {
		if time.Until(entry.expiresAt) <= 0 {
			log.Debugf("Expiring process info %+v. Time until expiration %v", entry, time.Until(entry.expiresAt))
			delete(r.cache, tuple)
			continue
		}
	}
}

func convertProtoEventToProcessInfo(event EventProtoStats) collector.ProcessInfo {
	srcIP := event.Saddr
	dstIP := event.Daddr
	sport := int(event.Sport)
	dport := int(event.Dport)
	tuple := tuple.Make(srcIP, dstIP, int(event.Proto), sport, dport)
	pname := bytes.Trim(event.ProcessName[:], "\x00")
	return collector.ProcessInfo{
		Tuple: tuple,
		ProcessData: collector.ProcessData{
			Name: string(pname),
			Pid:  int(event.Pid),
		},
	}
}

func convertTcpStatsEventToProcessInfo(event EventTcpStats) collector.ProcessInfo {
	srcIP := event.Saddr
	dstIP := event.Daddr
	sport := int(event.Sport)
	dport := int(event.Dport)
	tuple := tuple.Make(srcIP, dstIP, 6, sport, dport)
	return collector.ProcessInfo{
		Tuple: tuple,
		TcpStatsData: collector.TcpStatsData{
			SendCongestionWnd: event.SendCongestionWnd,
			SmoothRtt:         event.SmoothRtt,
			MinRtt:            event.MinRtt,
			Mss:               event.Mss,
			TotalRetrans:      event.TotalRetrans,
			LostOut:           event.LostOut,
			UnrecoveredRTO:    event.UnrecoveredRTO,
			IsDirty:           true,
		},
	}
}
