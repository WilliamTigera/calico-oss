---
title: Configuring encryption and authentication
---

## Connections from {{site.prodname}} components to etcd

If you are using the etcd datastore, we recommend enabling mutual TLS authentication on
its connections as follows.

- [Configure etcd](https://coreos.com/etcd/docs/latest/op-guide/security.html) to encrypt its
  communications with TLS and require clients to present certificates signed by the etcd certificate
  authority.

- Configure each {{site.prodname}} component to verify the etcd server's identity and to present
  a certificate to the etcd server that is signed by the etcd certificate authority.
  - [{{site.nodecontainer}}](../reference/node/configuration)
  - [`calicoctl`](./calicoctl/configure/etcd)
  - [CNI plugin](../reference/cni-plugin/configuration#etcd-location) (Kubernetes and OpenShift only)
  - [Kubernetes controllers](../reference/kube-controllers/configuration#configuring-etcd-access) (Kubernetes and OpenShift only)
  - [Felix](../reference/felix/configuration#etcd-datastore-configuration) (on [bare metal hosts](../getting-started/bare-metal/installation/))
  - [Typha](../reference/typha/configuration#etcd-datastore-configuration) (often deployed in
    larger Kubernetes deployments)

### Connections from {{site.prodname}} components to kube-apiserver (Kubernetes and OpenShift)

We recommend enabling TLS on kube-apiserver, as well as the client certificate and JSON web token (JWT)
authentication modules. This ensures that all of its communications with {{site.prodname}} components occur
over TLS. The {{site.prodname}} components present either an X.509 certificate or a JWT to kube-apiserver
so that kube-apiserver can verify their identities.

### Connections from Felix to Typha (Kubernetes)

We recommend enabling mutual TLS authentication on connections from Felix to Typha.
To do so, you must provision Typha with a server certificate and Felix with a client
certificate. Each service will need the private key associated with their certificate.
In addition, you must configure one of the following.

- **SPIFFE identifiers** (recommended): Generate a [SPIFFE](https://github.com/spiffe/spiffe) identifier for Felix,
  set `ClientURISAN` on Typha to Felix's SPIFFE ID, and include Felix's SPIFFE ID in the `URI SAN` field
  of its certificate. Similarly, generate a [SPIFFE](https://github.com/spiffe/spiffe) identifier for Typha,
  set `TyphaURISAN` on Felix to Typha's SPIFFE ID, and include Typha's SPIFFE ID in the `URI SAN` field
  of its certificate.

- **Common Name identifiers**: Configure `ClientCN` on Typha to the value in the `Common Name` of Felix's
  certificate. Configure `ClientCN` on Felix to the value in the `Common Name` of Typha's
  certificate.

> **Tip**: If you are migrating from Common Name to SPIFFE identifiers, you can set both values.
> If either matches, the communication succeeds.
{: .alert .alert-success}

Here is an example of how you can secure the Felix-Typha communications in your
cluster:

1.  Choose a certificate authority, or set up your own.

1.  Obtain or generate the following leaf certificates, signed by that
    authority, and corresponding keys:

    -  A certificate for each Felix with Common Name `typha-client` and
       extended key usage `ClientAuth`.

    -  A certificate for each Typha with Common Name `typha-server` and
       extended key usage `ServerAuth`.

1.  Configure each Typha with:

    -  `CAFile` pointing to the certificate authority certificate

    -  `ServerCertFile` pointing to that Typha's certificate

    -  `ServerKeyFile` pointing to that Typha's key

    -  `ClientCN` set to `typha-client`

    -  `ClientURISAN` unset.

1.  Configure each Felix with:

    -  `TyphaCAFile` pointing to the Certificate Authority certificate

    -  `TyphaCertFile` pointing to that Felix's certificate

    -  `TyphaKeyFile` pointing to that Felix's key

    -  `TyphaCN` set to `typha-server`

    -  `TyphaURISAN` unset.

For a [SPIFFE](https://github.com/spiffe/spiffe)-compliant deployment you can
follow the same procedure as above, except:

1.  Choose [SPIFFE
    Identities](https://github.com/spiffe/spiffe/blob/master/standards/SPIFFE-ID.md#2-spiffe-identity)
    to represent Felix and Typha.

1.  When generating leaf certificates for Felix and Typha, put the relevant
    SPIFFE Identity in the certificate as a URI SAN.

1.  Leave `ClientCN` and `TyphaCN` unset.

1.  Set Typha's `ClientURISAN` parameter to the SPIFFE Identity for Felix that
    you use in each Felix certificate.

1.  Set Felix's `TyphaURISAN` parameter to the SPIFFE Identity for Typha.

For detailed reference information on these parameters, refer to:

- **Typha**: [Felix-Typha TLS configuration](../reference/typha/configuration#felix-typha-tls-configuration)

- **Felix**: [Felix-Typha TLS configuration](../reference/felix/configuration#felix-typha-tls-configuration)

## {{site.prodname}} Manager connections

Tigera {{site.prodname}} Manager's web interface, run from your browser, uses HTTPS to securely communicate
with the {{site.prodname}} Manager, which in turn, communicates with the Kubernetes and {{site.prodname}} API
servers also using HTTPS. Through the installation steps, secure communication between
{{site.prodname}} components should already be configured, but secure communication through your web
browser of choice may not. To verify if this is properly configured, the web browser
you are using should display `Secure` in the address bar.

Before we set up TLS certificates, it is important to understand the traffic
that we are securing. By default, your web browser of choice communicates with
{{site.prodname}} Manager through a
[`NodePort` service](https://kubernetes.io/docs/tutorials/services/source-ip/#source-ip-for-services-with-typenodeport){:target="_blank"}
over port `30003`. The NodePort service passes through packets without modification.
TLS traffic is [terminated](https://en.wikipedia.org/wiki/TLS_termination_proxy){:target="_blank"}
at the {{site.prodname}} Manager. This means that the TLS certificates used to secure traffic
between your web browser and the {{site.prodname}} Manager do not need to be shared or related
to any other TLS certificates that may be used elsewhere in your cluster or when
configuring {{site.prodname}}. The flow of traffic should look like the following:

![{{site.prodname}} Manager traffic diagram]({{site.baseurl}}/images/cnx-tls-mgr-comms.svg){: width="60%" }

> **Note** the `NodePort` service in the above diagram can be replaced with other
> [Kubernetes services](https://kubernetes.io/docs/concepts/services-networking/service/#publishing-services---service-types){:target="_blank"}.
> Configuration will vary if another service, such as a load balancer, is placed between the web
> browser and the {{site.prodname}} Manager.
{: .alert .alert-info}

In order to properly configure TLS in the {{site.prodname}} Manager, you will need
certificates and keys signed by an appropriate Certificate Authority (CA).
For more high level information on certificates, keys, and CAs, see
[this blogpost](https://blog.talpor.com/2015/07/ssltls-certificates-beginners-tutorial){:target="_blank"}.

> **Note** It is important when generating your certificates to make sure
> that the Common Name or Subject Alternative Name specified in your certificates
> matches the host name/DNS entry/IP address that is used to access the {{site.prodname}} Manager
> (i.e. what it says in the browser address bar).
{: .alert .alert-info}

Once you have the proper server certificates, you will need to add them to the
{{site.prodname}} Manager. During installation of the {{site.prodname}} Manager, you should have run
the following command.

```
sudo kubectl create secret generic cnx-manager-tls --from-file=cert=/etc/kubernetes/pki/apiserver.crt \
--from-file=key=/etc/kubernetes/pki/apiserver.key -n kube-system
```
> **Note** If you are using certificates not from a third party CA,
> you will need to also add your certificates to your web browser.
> See the `Troubleshooting` section for details.

The `.crt` and `.key` files should be the TLS certificate and key respectively
that you are using for securing the traffic with TLS. If you need to replace the
certificates that you specified during installation, rerunning this command while
specifying the correct files will fix the issue. Once the certificates are updated,
you will need to kill the {{site.prodname}} Manager pod so that it is restarted to uptake the new
certificates.

```
kubectl delete pod <cnx-manager-pod-name> -n kube-system
```

If your web browser still does not display `Secure` in the address bar, the most
common reasons and their fixes are listed below.

- **Untrusted Certificate Authority**: Your browser may not display `Secure` because
  it does not know (and therefore trust) the certificate authority (CA) that issued
  the certificates that the {{site.prodname}} Manager is using. This is generally caused by using
  self-signed certificates (either generated by Kubernetes or manually). If you have
  certificates signed by a recognized CA, we recommend that you use them with the {{site.prodname}}
  Manager since the browser will automatically recognize them.

  If you opt to use self-signed certificates you can still configure your browser to
  trust the CA on a per-browser basis by importing the CA certificates into the browser.
  In Google Chrome, this can be achieved by selecting Settings, Advanced, Privacy and security,
  Manage certificates, Authorities, Import. This is not recommended since it requires the CA
  to be imported into every browser you access {{site.prodname}} Manager from.

- **Mismatched Common Name or Subject Alternative Name**: If you are still having issues
  securely accessing {{site.prodname}} Manager with TLS, you may want to make sure that the Common Name
  or Subject Alternative Name specified in your certificates matches the host name/DNS
  entry/IP address that is used to access the {{site.prodname}} Manager (i.e. what it says in the browser
  address bar). In Google Chrome you can check the {{site.prodname}} Manager certificate with Developer Tools
  (Ctrl+Shift+I), Security. If you are issued certificates which do not match,
  you will need to reissue the certificates with the correct Common Name or
  Subject Alternative Name and reconfigure {{site.prodname}} Manager following the steps above.
