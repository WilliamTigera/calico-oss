// Copyright 2021 Tigera Inc. All rights reserved.

package alert

import (
	"context"
	"reflect"
	"time"

	log "github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"

	v3 "github.com/tigera/api/pkg/apis/projectcalico/v3"
	calicoclient "github.com/tigera/api/pkg/client/clientset_generated/clientset"
	es "github.com/tigera/intrusion-detection/controller/pkg/globalalert/elastic"
	lma "github.com/tigera/lma/pkg/elastic"
)

type Alert struct {
	alert       *v3.GlobalAlert
	calicoCLI   calicoclient.Interface
	es          es.Service
	clusterName string
}

const (
	DefaultPeriod      = 5 * time.Minute
	MinimumAlertPeriod = 5 * time.Second
)

// NewAlert sets and returns an Alert, builds Elasticsearch query that will be used periodically to query Elasticsearch data.
func NewAlert(alert *v3.GlobalAlert, calicoCLI calicoclient.Interface, lmaESClient lma.Client, clusterName string) (*Alert, error) {
	alert.Status.Active = true
	alert.Status.LastUpdate = &metav1.Time{Time: time.Now()}

	es, err := es.NewService(lmaESClient, clusterName, alert)
	if err != nil {
		return nil, err
	}

	return &Alert{
		alert:       alert,
		calicoCLI:   calicoCLI,
		es:          es,
		clusterName: clusterName,
	}, nil
}

// Execute periodically queries the Elasticsearch, updates GlobalAlert status
// and adds alerts to events index if alert conditions are met.
// If parent context is cancelled, updates the GlobalAlert status and returns.
// It also deletes any existing elastic watchers for the cluster.
func (a *Alert) Execute(ctx context.Context) {
	a.es.DeleteElasticWatchers(ctx)
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error { return a.updateStatus(ctx) }); err != nil {
		log.WithError(err).Errorf("failed to update status GlobalAlert %s in cluster %s, maximum retries reached", a.alert.Name, a.clusterName)
	}

	for {
		timer := time.NewTimer(a.getDurationUntilNextAlert())
		select {
		case <-timer.C:
			a.alert.Status = a.es.ExecuteAlert(a.alert)
			if err := retry.RetryOnConflict(retry.DefaultRetry, func() error { return a.updateStatus(ctx) }); err != nil {
				log.WithError(err).Errorf("failed to update status GlobalAlert %s in cluster %s, maximum retries reached", a.alert.Name, a.clusterName)
			}
			timer.Stop()
		case <-ctx.Done():
			a.alert.Status.Active = false
			if err := retry.RetryOnConflict(retry.DefaultRetry, func() error { return a.updateStatus(ctx) }); err != nil {
				log.WithError(err).Errorf("failed to update status GlobalAlert %s in cluster %s, maximum retries reached", a.alert.Name, a.clusterName)
			}
			timer.Stop()
			return
		}
	}
}

// getDurationUntilNextAlert returns the duration after which next alert should run.
// If Status.LastExecuted is set on alert use that to calculate time for next alert execution,
// else uses Spec.Period value.
func (a *Alert) getDurationUntilNextAlert() time.Duration {
	alertPeriod := DefaultPeriod
	if a.alert.Spec.Period != nil {
		alertPeriod = a.alert.Spec.Period.Duration
	}

	if a.alert.Status.LastExecuted != nil {
		now := time.Now()
		durationSinceLastExecution := now.Sub(a.alert.Status.LastExecuted.Local())
		if durationSinceLastExecution < 0 {
			log.Errorf("last executed alert is in the future")
			return MinimumAlertPeriod
		}
		timeUntilNextRun := alertPeriod - durationSinceLastExecution
		if timeUntilNextRun <= 0 {
			// return MinimumAlertPeriod instead of 0s to guarantee that we would never have a tight loop
			// that burns through our pod resources and spams Elasticsearch.
			return MinimumAlertPeriod
		}
		return timeUntilNextRun
	}
	return alertPeriod
}

// updateStatus gets the latest GlobalAlert and updates its status.
func (a *Alert) updateStatus(ctx context.Context) error {
	log.Debugf("Updating status of GlobalAlert %s in cluster %s", a.alert.Name, a.clusterName)
	alert, err := a.calicoCLI.ProjectcalicoV3().GlobalAlerts().Get(ctx, a.alert.Name, metav1.GetOptions{})
	if err != nil {
		log.WithError(err).Errorf("could not get GlobalAlert %s in cluster %s", a.alert.Name, a.clusterName)
		return err
	}
	alert.Status = a.alert.Status
	_, err = a.calicoCLI.ProjectcalicoV3().GlobalAlerts().UpdateStatus(ctx, alert, metav1.UpdateOptions{})
	if err != nil {
		log.WithError(err).Errorf("could not update status of GlobalAlert %s in cluster %s", a.alert.Name, a.clusterName)
		return err
	}
	return nil
}

// EqualAlertSpec does reflect.DeepEqual on give spec and cached alert spec.
func (a *Alert) EqualAlertSpec(spec v3.GlobalAlertSpec) bool {
	return reflect.DeepEqual(a.alert.Spec, spec)
}
