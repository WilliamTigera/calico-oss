// Copyright (c) 2025 Tigera, Inc. All rights reserved.

package checkpoint_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	bapi "github.com/projectcalico/calico/linseed/pkg/backend/api"
	"github.com/projectcalico/calico/oiler/pkg/checkpoint"
	"github.com/projectcalico/calico/oiler/pkg/migrator/operator"
)

func TestConfigMapStorage_Write(t *testing.T) {

	t.Run("Create config map if it does not exist", func(t *testing.T) {
		client := fake.NewClientset()

		namespace := "any-namespace"
		name := "any-name"
		checkpointTime := time.Unix(1, 0).UTC()
		c := checkpoint.NewConfigMapStorage(client, namespace, name)
		err := c.Write(context.Background(), checkpointTime)
		require.NoError(t, err)

		cm, err := client.CoreV1().ConfigMaps(namespace).Get(context.Background(), name, metav1.GetOptions{})
		require.NoError(t, err)
		require.NotNil(t, cm)
		require.NotEmpty(t, cm.Data)
		checkpointVal, ok := cm.Data["checkpoint"]
		require.True(t, ok)
		require.Equal(t, checkpointVal, checkpointTime.Format(time.RFC3339))
	})

	t.Run("Update config map", func(t *testing.T) {
		client := fake.NewClientset()

		namespace := "any-namespace"
		name := "any-name"
		firstCheckpointTime := time.Unix(1, 0).UTC()
		secondCheckpointTime := time.Unix(2, 0).UTC()

		c := checkpoint.NewConfigMapStorage(client, namespace, name)
		err := c.Write(context.Background(), firstCheckpointTime)
		require.NoError(t, err)

		err = c.Write(context.Background(), secondCheckpointTime)
		require.NoError(t, err)

		cm, err := client.CoreV1().ConfigMaps(namespace).Get(context.Background(), name, metav1.GetOptions{})
		require.NoError(t, err)
		require.NotNil(t, cm)
		require.NotEmpty(t, cm.Data)
		checkpointVal, ok := cm.Data["checkpoint"]
		require.True(t, ok)
		require.Equal(t, checkpointVal, secondCheckpointTime.Format(time.RFC3339))
	})
}

func TestConfigMapStorage_Read(t *testing.T) {
	tests := []struct {
		name      string
		configMap *v1.ConfigMap
		want      operator.TimeInterval
		wantErr   error
	}{
		{
			name: "valid config map",
			configMap: &v1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
				Name:      checkpoint.ConfigMapName(bapi.FlowLogs, "any", "any"),
				Namespace: "any",
			},
				Data: map[string]string{"checkpoint": time.Unix(1, 0).UTC().Format(time.RFC3339)},
			},
			want: operator.TimeInterval{Start: ptrTime(time.Unix(1, 0).UTC())},
		},
		{
			name: "malformed checkpoint",
			configMap: &v1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
				Name:      checkpoint.ConfigMapName(bapi.FlowLogs, "any", "any"),
				Namespace: "any",
			},
				Data: map[string]string{"checkpoint": "abc"},
			},
			wantErr: errors.New("parsing time \"abc\" as \"2006-01-02T15:04:05Z07:00\": cannot parse \"abc\" as \"2006\""),
		},
		{
			name: "empty data",
			configMap: &v1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
				Name:      checkpoint.ConfigMapName(bapi.FlowLogs, "any", "any"),
				Namespace: "any",
			},
				Data: map[string]string{},
			},
			want: operator.TimeInterval{},
		},
		{
			name: "missing data",
			configMap: &v1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
				Name:      checkpoint.ConfigMapName(bapi.FlowLogs, "any", "any"),
				Namespace: "any",
			},
			},
			want: operator.TimeInterval{},
		},
		{
			name: "missing config map",
			want: operator.TimeInterval{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := fake.NewClientset()
			if tt.configMap != nil {
				_, err := client.CoreV1().ConfigMaps("any").Create(context.Background(), tt.configMap, metav1.CreateOptions{})
				require.NoError(t, err)
			}

			c := checkpoint.NewConfigMapStorage(client, "any",
				checkpoint.ConfigMapName(bapi.FlowLogs, "any", "any"))
			checkpointVal, err := c.Read(context.Background())
			if tt.wantErr != nil {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, checkpointVal, tt.want)
			}
		})
	}
}

func ptrTime(time time.Time) *time.Time {
	return &time
}

func TestConfigMapName(t *testing.T) {
	tests := []struct {
		name     string
		dataType bapi.DataType
		cluster  string
		tenant   string
		want     string
	}{
		{
			name:     "empty tenant",
			cluster:  "anyCluster",
			tenant:   "",
			dataType: bapi.FlowLogs,
			want:     "flows-anyCluster",
		},
		{
			name:     "full name",
			cluster:  "anyCluster",
			tenant:   "anyTenant",
			dataType: bapi.FlowLogs,
			want:     "flows-anyCluster-anyTenant",
		},
		{
			name:     "audit ee data type",
			cluster:  "anyCluster",
			tenant:   "anyTenant",
			dataType: bapi.AuditEELogs,
			want:     "audit-ee-anyCluster-anyTenant",
		},
		{
			name:     "audit kube data type",
			cluster:  "anyCluster",
			tenant:   "anyTenant",
			dataType: bapi.AuditKubeLogs,
			want:     "audit-kube-anyCluster-anyTenant",
		},
		{
			name:     "compliance reports",
			cluster:  "anyCluster",
			tenant:   "anyTenant",
			dataType: bapi.ReportData,
			want:     "compliance-reports-anyCluster-anyTenant",
		},
		{
			name:     "DNS threat feeds",
			cluster:  "anyCluster",
			tenant:   "anyTenant",
			dataType: bapi.DomainNameSet,
			want:     "threatfeeds-domainnameset-anyCluster-anyTenant",
		},
		{
			name:     "IP threat feeds",
			cluster:  "anyCluster",
			tenant:   "anyTenant",
			dataType: bapi.IPSet,
			want:     "threatfeeds-ipset-anyCluster-anyTenant",
		},
		{
			name:     "Benchmarks",
			cluster:  "anyCluster",
			tenant:   "anyTenant",
			dataType: bapi.Benchmarks,
			want:     "benchmark-results-anyCluster-anyTenant",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := checkpoint.ConfigMapName(tt.dataType, tt.cluster, tt.tenant); got != tt.want {
				t.Errorf("ConfigMapName() = %v, want %v", got, tt.want)
			}
		})
	}
}
