// Copyright (c) 2021 Tigera, Inc. All rights reserved.

package middleware

import "context"

type key int

const (
	namespaceKey key = iota
	captureKey
	clusterIDKey
)

// WithCaptureName sets the packet capture name on the context of a request
func WithCaptureName(ctx context.Context, captureName string) context.Context {
	return context.WithValue(ctx, captureKey, captureName)
}

// WithNamespace sets the packet capture namespace on the context of a request
func WithNamespace(ctx context.Context, captureNamespace string) context.Context {
	return context.WithValue(ctx, namespaceKey, captureNamespace)
}

// WithClusterID sets the x-cluster-id identifier on the context of a request
func WithClusterID(ctx context.Context, clusterID string) context.Context {
	return context.WithValue(ctx, clusterIDKey, clusterID)
}

// CaptureNameFromContext retrieves the packet capture name from the context
func CaptureNameFromContext(ctx context.Context) string {
	v := ctx.Value(captureKey)
	if v == nil {
		return ""
	}
	return v.(string)
}

// NamespaceFromContext retrieves the packet capture namespace from the context
func NamespaceFromContext(ctx context.Context) string {
	v := ctx.Value(namespaceKey)
	if v == nil {
		return ""
	}
	return v.(string)
}

// ClusterIDFromContext retrieves the cluster id from the context
func ClusterIDFromContext(ctx context.Context) string {
	v := ctx.Value(clusterIDKey)
	if v == nil {
		return ""
	}
	return v.(string)
}
