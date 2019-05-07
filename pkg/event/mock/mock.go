package mock

import (
	"context"
	"encoding/json"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	auditv1 "k8s.io/apiserver/pkg/apis/audit"

	"github.com/tigera/compliance/pkg/event"
	"github.com/tigera/compliance/pkg/resources"
)

type Fetcher struct {
	data map[metav1.TypeMeta][]*event.AuditEventResult
}

func NewEventFetcher() *Fetcher {
	return &Fetcher{data: make(map[metav1.TypeMeta][]*event.AuditEventResult)}
}

func (f *Fetcher) GetAuditEvents(ctx context.Context, tm *metav1.TypeMeta, from, to *time.Time) <-chan *event.AuditEventResult {
	ch := make(chan *event.AuditEventResult)
	go func() {
		defer close(ch)

		// All type metas.
		if tm == nil {
			for _, events := range f.data {
				for _, ev := range events {
					if ev.StageTimestamp.Time.After(*from) && ev.StageTimestamp.Time.Before(*to) {
						ch <- ev
					}
				}
			}
			return
		}

		// Single type meta.
		for _, ev := range f.data[*tm] {
			if ev.StageTimestamp.Time.After(*from) && ev.StageTimestamp.Time.Before(*to) {
				ch <- ev
			}
		}
	}()
	return ch
}

func (f *Fetcher) LoadAuditEvent(verb string, objRef resources.Resource, respObj interface{}, timestamp time.Time, resVer string) {
	ev := new(auditv1.Event)
	ev.Verb = verb

	// Get the resource helper.
	tm := resources.GetTypeMeta(objRef)
	rh := resources.GetResourceHelperByTypeMeta(tm)

	// Set the objectRef
	ev.ObjectRef = &auditv1.ObjectReference{
		Name:       objRef.GetObjectMeta().GetName(),
		Namespace:  objRef.GetObjectMeta().GetNamespace(),
		APIGroup:   objRef.GetObjectKind().GroupVersionKind().Group,
		APIVersion: objRef.GetObjectKind().GroupVersionKind().Version,
		Resource:   rh.Plural(),
	}

	// Set the resource version
	objRef.GetObjectMeta().SetResourceVersion(resVer)

	// Set the response object.
	resJson, err := json.Marshal(respObj)
	ev.ResponseObject = &runtime.Unknown{Raw: resJson}
	if err != nil {
		panic(err)
	}

	// Set the timestamp.
	ev.StageTimestamp = metav1.MicroTime{timestamp}

	// Append to event array
	f.data[tm] = append(f.data[tm], &event.AuditEventResult{ev, nil})
}
