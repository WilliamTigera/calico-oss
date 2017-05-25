// Copyright (c) 2016 Tigera, Inc. All rights reserved.

package commands

import (
	"fmt"
	"os"

	log "github.com/Sirupsen/logrus"
	"github.com/projectcalico/felix/dispatcher"
	"github.com/projectcalico/felix/labelindex"
	"github.com/projectcalico/libcalico-go/lib/backend"
	"github.com/projectcalico/libcalico-go/lib/backend/api"
	"github.com/projectcalico/libcalico-go/lib/backend/model"
	"github.com/projectcalico/libcalico-go/lib/client"
	"github.com/projectcalico/libcalico-go/lib/selector"
)

func EvalSelector(sel string) (err error) {
	cbs := NewEvalCmd()
	cbs.AddSelector("the selector", sel)
	noopFilter := func(update api.Update) (filterOut bool) {
		return false
	}
	cbs.Start(noopFilter)

	matches := cbs.GetMatches()
	fmt.Printf("Endpoints matching selector %v:\n", sel)
	for endpoint := range matches {
		fmt.Printf("  %v\n", endpointName(endpoint))
	}
	return
}

// Restructuring like this should also be useful for a permanently running webserver variant too.
func NewEvalCmd() (cbs *EvalCmd) {
	disp := dispatcher.NewDispatcher()
	cbs = &EvalCmd{
		dispatcher: disp,
		done:       make(chan bool),
		matches:    make(map[interface{}][]string),
	}
	cbs.index = labelindex.NewInheritIndex(cbs.onMatchStarted, cbs.onMatchStopped)
	return cbs
}

func (cbs *EvalCmd) AddSelector(selectorName string, selectorExpression string) {
	parsedSel, err := selector.Parse(selectorExpression)
	if err != nil {
		fmt.Printf("Invalid selector: %#v. %v.\n", selectorExpression, err)
		os.Exit(1)
	}

	cbs.index.UpdateSelector(selectorName, parsedSel)
}

// Call this once you've AddSelector'ed the selectors you want to add.
// We'll always do checkValid, but allow insertion of an additional EP filter.
func (cbs *EvalCmd) Start(endpointFilter dispatcher.UpdateHandler) {
	checkValid := func(update api.Update) (filterOut bool) {
		if update.Value == nil {
			fmt.Printf("WARNING: failed to parse value of key %v; "+
				"ignoring.\n\n\n", update)
			return true
		}
		return false
	}

	cbs.dispatcher.Register(model.WorkloadEndpointKey{}, checkValid)
	cbs.dispatcher.Register(model.HostEndpointKey{}, checkValid)

	cbs.dispatcher.Register(model.WorkloadEndpointKey{}, endpointFilter)
	cbs.dispatcher.Register(model.HostEndpointKey{}, endpointFilter)

	cbs.dispatcher.Register(model.WorkloadEndpointKey{}, cbs.OnUpdate)
	cbs.dispatcher.Register(model.HostEndpointKey{}, cbs.OnUpdate)
	cbs.dispatcher.Register(model.ProfileLabelsKey{}, cbs.OnUpdate)

	apiConfig, err := client.LoadClientConfig("")
	if err != nil {
		log.Fatal("Failed loading client config")
		os.Exit(1)
	}
	bclient, err := backend.NewClient(*apiConfig)
	if err != nil {
		log.Fatal("Failed to create client")
		os.Exit(1)
	}
	syncer := bclient.Syncer(cbs)
	syncer.Start()
}

// For the final version of this we probably will want to be able to call AddSelector()
// while everything's running and join this function into that so you make the call and
// then a little bit later it returns the results.
// Ideally it probably wouldn't even have an AddSelector(), and this could just be accomplished
// with some custom filter functions on a single dispatcher.
// However solving that now would be more effort, so for this version everything must be
// added before Start()ing.
// Returns a map from endpoint key (model.Host/WorkloadEndpointKey) to a list of strings containing the
// names of the selectors that matched them.
func (cbs *EvalCmd) GetMatches() map[interface{}][]string {
	<-cbs.done
	return cbs.matches
}

func endpointName(key interface{}) string {
	var epName string
	switch epID := key.(type) {
	case model.WorkloadEndpointKey:
		epName = fmt.Sprintf("Workload endpoint %v/%v/%v/%v", epID.Hostname, epID.OrchestratorID, epID.WorkloadID, epID.EndpointID)
	case model.HostEndpointKey:
		epName = fmt.Sprintf("Host endpoint %v/%v", epID.Hostname, epID.EndpointID)
	}
	return epName
}

type EvalCmd struct {
	dispatcher *dispatcher.Dispatcher
	index      *labelindex.InheritIndex
	matches    map[interface{}][]string

	done chan bool
}

func (cbs *EvalCmd) OnConfigLoaded(globalConfig map[string]string,
	hostConfig map[string]string) {
	// Ignore for now
}

func (cbs *EvalCmd) OnStatusUpdated(status api.SyncStatus) {
	if status == api.InSync {
		log.Info("Datamodel in sync, we're done.")
		cbs.done <- true
	}
}

func (cbs *EvalCmd) OnKeysUpdated(updates []api.Update) {
	log.Info("Update: ", updates)
	for _, update := range updates {
		// Also removed empty key handling: don't understand it.
		cbs.dispatcher.OnUpdate(update)
	}
}

func (cbs *EvalCmd) OnUpdate(update api.Update) (filterOut bool) {
	if update.Value == nil {
		return true
	}
	switch k := update.Key.(type) {
	case model.WorkloadEndpointKey:
		v := update.Value.(*model.WorkloadEndpoint)
		cbs.index.UpdateLabels(update.Key, v.Labels, v.ProfileIDs)
	case model.HostEndpointKey:
		v := update.Value.(*model.HostEndpoint)
		cbs.index.UpdateLabels(update.Key, v.Labels, v.ProfileIDs)
	case model.ProfileLabelsKey:
		v := update.Value.(map[string]string)
		cbs.index.UpdateParentLabels(k.Name, v)
	default:
		log.Errorf("Unexpected update type: %#v", update)
		return true
	}
	return false
}

func (cbs *EvalCmd) OnUpdates(updates []api.Update) {
	log.Info("Update: ", updates)
	for _, update := range updates {
		// MATT: Removed some handling of empty key: don't understand how it can happen.
		cbs.dispatcher.OnUpdate(update)
	}
}

func (cbs *EvalCmd) onMatchStarted(selId, epId interface{}) {
	if pols, ok := cbs.matches[epId]; ok {
		cbs.matches[epId] = append(pols, selId.(string))
	} else {
		cbs.matches[epId] = []string{selId.(string)}
	}
}

func (cbs *EvalCmd) onMatchStopped(selId, epId interface{}) {
	log.Errorf("Unexpected match stopped event: %v, %v", selId, epId)
}
