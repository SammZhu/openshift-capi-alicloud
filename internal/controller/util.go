package controller

import (
	"context"
	"time"

	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	infrav1 "github.com/openshift/cluster-api-provider-alibaba/api/v1beta1"
)

const requeueAfter = 30 * time.Second

// enqueueAlibabaCloudClusterForCluster returns a handler that enqueues
// AlibabaCloudCluster reconcile requests when the owning Cluster changes.
func enqueueAlibabaCloudClusterForCluster(_ client.Client) handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
		cluster, ok := obj.(*clusterv1.Cluster)
		if !ok {
			return nil
		}
		// InfrastructureRef is a struct in v1beta2, check if empty instead of nil
		if cluster.Spec.InfrastructureRef.Kind == "" || cluster.Spec.InfrastructureRef.Name == "" {
			return nil
		}
		// Check that the infra ref points to our provider group
		if cluster.Spec.InfrastructureRef.APIGroup != infrav1.GroupVersion.Group {
			return nil
		}
		return []reconcile.Request{
			{
				NamespacedName: client.ObjectKey{
					Namespace: cluster.Namespace,
					Name:      cluster.Spec.InfrastructureRef.Name,
				},
			},
		}
	})
}
