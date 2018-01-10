- The aggregation layer of kube-apiserver must be enabled. Refer to the 
  [Kubernetes documentation](https://kubernetes.io/docs/tasks/access-kubernetes-api/configure-aggregation-layer/)
  for details. 
  
- [Select a supported authentication method](../reference/cnx/authentication) 
  and [configure kube-apiserver](https://kubernetes.io/docs/admin/authentication/) accordingly.
  
- Ensure that kube-apiserver allows TLS communications, which it usually
  does by default. Refer to the [Kubernetes documentation](https://kubernetes.io/docs/admin/accessing-the-api/)
  for more information.
