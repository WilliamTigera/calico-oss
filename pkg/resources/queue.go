// Copyright (c) 2019 Tigera, Inc. All rights reserved.
package resources

import (
	"container/heap"

	apiv3 "github.com/projectcalico/libcalico-go/lib/apis/v3"
)

// An QueueItem is an item in a resources.PriorityQueue used to prioritise apiv3.ResourceID actions.
type QueueItem struct {
	// The ID of the resource requiring recalculation.
	ResourceID apiv3.ResourceID

	// The Priority of this queue entry.
	Priority int8

	// The index is needed by update and is maintained by the heap.Interface methods.
	index int
}

// A PriorityQueue implements heap.Interface and holds QueueItems.
type PriorityQueue []*QueueItem

func (pq PriorityQueue) Len() int { return len(pq) }

func (pq PriorityQueue) Less(i, j int) bool {
	// We want Pop to give us the highest, not lowest, Priority so we use greater than here.
	return pq[i].Priority > pq[j].Priority
}

func (pq PriorityQueue) Swap(i, j int) {
	pq[i], pq[j] = pq[j], pq[i]
	pq[i].index = i
	pq[j].index = j
}

func (pq *PriorityQueue) Push(x interface{}) {
	n := len(*pq)
	item := x.(*QueueItem)
	item.index = n
	*pq = append(*pq, item)
}

func (pq *PriorityQueue) Pop() interface{} {
	old := *pq
	n := len(old)
	item := old[n-1]
	item.index = -1 // for safety
	*pq = old[0 : n-1]
	return item
}

// update modifies the Priority and value of an QueueItem in the queue.
func (pq *PriorityQueue) update(item *QueueItem, id apiv3.ResourceID, priority int8) {
	item.ResourceID = id
	item.Priority = priority
	heap.Fix(pq, item.index)
}
