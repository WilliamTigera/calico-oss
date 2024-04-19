// Copyright (c) 2023 Tigera, Inc. All rights reserved.

package token

import (
	"context"
	"crypto/rsa"
	"errors"
	"fmt"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	"k8s.io/client-go/informers"

	"github.com/projectcalico/calico/kube-controllers/pkg/controllers/utils"

	"k8s.io/apiserver/pkg/authentication/user"

	"github.com/SermoDigital/jose/jws"
	"github.com/golang-jwt/jwt/v4"
	"github.com/sirupsen/logrus"
	v3 "github.com/tigera/api/pkg/apis/projectcalico/v3"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"

	"github.com/projectcalico/calico/kube-controllers/pkg/resource"
	"github.com/projectcalico/calico/libcalico-go/lib/health"
	"github.com/projectcalico/calico/lma/pkg/k8s"
)

const (
	LinseedIssuer        string = "linseed.tigera.io"
	defaultTokenLifetime        = 24 * time.Hour
	allClustersRetryKey         = "all-clusters-retry-key"
)

type Controller interface {
	Run(<-chan struct{})
}

type ControllerOption func(*controller) error

func WithPrivateKey(k *rsa.PrivateKey) ControllerOption {
	return func(c *controller) error {
		c.privateKey = k
		return nil
	}
}

// WithControllerRuntimeClient configures the controller runtime client used to access managed cluster resources.
func WithControllerRuntimeClient(client ctrlclient.WithWatch) ControllerOption {
	return func(c *controller) error {
		c.client = client
		return nil
	}
}

func WithK8sClient(kc kubernetes.Interface) ControllerOption {
	return func(c *controller) error {
		c.managementK8sClient = kc
		return nil
	}
}

func WithSecretsToCopy(secrets []corev1.Secret) ControllerOption {
	return func(c *controller) error {
		c.secretsToCopy = secrets
		return nil
	}
}

func WithTenant(tenant string) ControllerOption {
	return func(c *controller) error {
		c.tenant = tenant
		return nil
	}
}

func WithTenantNamespaceForUsers(tenant string) ControllerOption {
	return func(c *controller) error {
		c.tenant = tenant
		return nil
	}
}

func WithImpersonation(info *user.DefaultInfo) ControllerOption {
	return func(c *controller) error {
		c.impersonationInfo = info
		return nil
	}
}

// WithIssuer sets the issuer of the generated tokens.
func WithIssuer(iss string) ControllerOption {
	return func(c *controller) error {
		c.issuer = iss
		return nil
	}
}

// WithIssuerName sets the name of the token issuer, used when generating
// names for token secrets in managed clusters.
func WithIssuerName(name string) ControllerOption {
	return func(c *controller) error {
		c.issuerName = name
		return nil
	}
}

// WithExpiry sets the duration that generated tokens should be valid for.
func WithExpiry(d time.Duration) ControllerOption {
	return func(c *controller) error {
		c.expiry = d
		return nil
	}
}

// WithFactory sets the factory to use for generating per-cluster clients.
func WithFactory(f k8s.ClientSetFactory) ControllerOption {
	return func(c *controller) error {
		c.factory = f
		return nil
	}
}

type UserInfo struct {
	Name                    string
	Namespace               string
	TenantNamespaceOverride string
}

// WithUserInfos sets the users in each managed cluster that this controller
// should generate tokens for.
func WithUserInfos(s []UserInfo) ControllerOption {
	return func(c *controller) error {
		for _, sa := range s {
			if sa.Name == "" {
				return fmt.Errorf("missing Name field in UserInfo")
			}
			if sa.Namespace == "" {
				return fmt.Errorf("missing Namespace field in UserInfo")
			}

		}
		c.userInfos = s
		return nil
	}
}

func WithReconcilePeriod(t time.Duration) ControllerOption {
	return func(c *controller) error {
		c.reconcilePeriod = &t
		return nil
	}
}

// WithBaseRetryPeriod sets the base retry period for retrying failed operations.
// The actual retry period is calculated as baseRetryPeriod * 2^retryCount.
func WithBaseRetryPeriod(t time.Duration) ControllerOption {
	return func(c *controller) error {
		c.baseRetryPeriod = &t
		return nil
	}
}

func WithMaxRetries(n int) ControllerOption {
	return func(c *controller) error {
		c.maxRetries = &n
		return nil
	}
}

func WithHealthReport(reportHealth func(*health.HealthReport)) ControllerOption {
	return func(c *controller) error {
		c.reportHealth = reportHealth
		return nil
	}
}

func WithNamespace(ns string) ControllerOption {
	return func(c *controller) error {
		c.namespace = ns
		return nil
	}
}

func NewController(opts ...ControllerOption) (Controller, error) {
	c := &controller{}
	for _, opt := range opts {
		if err := opt(c); err != nil {
			return nil, err
		}
	}

	// Default anything not set.
	if c.reconcilePeriod == nil {
		d := 60 * time.Minute
		c.reconcilePeriod = &d
	}
	if c.baseRetryPeriod == nil {
		d := 1 * time.Second
		c.baseRetryPeriod = &d
	}
	if c.maxRetries == nil {
		n := 20
		c.maxRetries = &n
	}

	// Verify necessary options set.
	if c.client == nil {
		return nil, fmt.Errorf("must provide a management cluster controller runtime client")
	}
	if c.privateKey == nil {
		return nil, fmt.Errorf("must provide a private key")
	}
	if c.issuer == "" {
		return nil, fmt.Errorf("must provide an issuer")
	}
	if c.issuerName == "" {
		return nil, fmt.Errorf("must provide an issuer name")
	}
	if len(c.userInfos) == 0 {
		return nil, fmt.Errorf("must provide at least one user info")
	}
	if c.factory == nil {
		return nil, fmt.Errorf("must provide a clientset factory")
	}
	if c.managementK8sClient == nil {
		return nil, fmt.Errorf("must provide a management Kubernetes client")
	}
	return c, nil
}

type controller struct {
	// Input configuration.
	privateKey          *rsa.PrivateKey
	tenant              string
	namespace           string
	issuer              string
	issuerName          string
	client              ctrlclient.WithWatch
	managementK8sClient kubernetes.Interface
	secretsToCopy       []corev1.Secret
	expiry              time.Duration
	reconcilePeriod     *time.Duration
	baseRetryPeriod     *time.Duration
	maxRetries          *int
	reportHealth        func(*health.HealthReport)
	factory             k8s.ClientSetFactory

	// userInfos in the managed cluster that we should provision tokens for.
	userInfos []UserInfo

	// impersonationInfo contains the information necessary to populate the HTTP impersonation headers needed to perform
	// certain actions on behalf of the managed cluster (eg. copying secrets)
	impersonationInfo *user.DefaultInfo
}

func (c *controller) Run(stopCh <-chan struct{}) {
	// TODO: Support multiple copies of this running.

	// Start a watch on ManagedClusters, wait for it to sync, and then proceed.
	// We'll trigger events whenever a new cluster is added, causing us to check whether
	// we need to provision token secrets in that cluster.
	logrus.Info("Starting token controller")

	// Make channels for sending updates.
	mcChan := make(chan *v3.ManagedCluster, 100)
	secretChan := make(chan *corev1.Secret, 100)
	defer close(mcChan)
	defer close(secretChan)

	managedClusterHandler := cache.ResourceEventHandlerFuncs{
		DeleteFunc: func(obj interface{}) {},
		AddFunc: func(obj interface{}) {
			if mc, ok := obj.(*v3.ManagedCluster); ok && isConnected(mc) {
				mcChan <- mc
			}
		},
		UpdateFunc: func(_, obj interface{}) {
			if mc, ok := obj.(*v3.ManagedCluster); ok && isConnected(mc) {
				mcChan <- mc
			}
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	listWatcher := newManagedClusterListWatcher(ctx, c.client, c.namespace)
	mcInformer := cache.NewSharedIndexInformer(listWatcher, &v3.ManagedCluster{}, 0, cache.Indexers{})
	_, err := mcInformer.AddEventHandler(managedClusterHandler)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to add ManagedCluster event handler")
	}

	secretFactory := informers.NewSharedInformerFactory(c.managementK8sClient, 0)
	secretInformer := secretFactory.Core().V1().Secrets().Informer()
	secretHandler := cache.ResourceEventHandlerFuncs{
		DeleteFunc: func(obj interface{}) {}, // TODO: Clean up deleted secrets in the managed cluster
		AddFunc: func(obj interface{}) {
			if s, ok := obj.(*corev1.Secret); ok {
				for _, secret := range c.secretsToCopy {
					if s.Name == secret.Name && s.Namespace == secret.Namespace {
						secretChan <- s
						break
					}
				}
			}
		},
		UpdateFunc: func(_, obj interface{}) {
			if s, ok := obj.(*corev1.Secret); ok {
				for _, secret := range c.secretsToCopy {
					if s.Name == secret.Name && s.Namespace == secret.Namespace {
						secretChan <- s
						break
					}
				}
			}
		},
	}
	_, err = secretInformer.AddEventHandler(secretHandler)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to add Secret event handler")
	}

	go mcInformer.Run(stopCh)
	go secretInformer.Run(stopCh)

	logrus.Info("Waiting for token controller to sync with ManagedCluster informer")
	for !mcInformer.HasSynced() {
		time.Sleep(1 * time.Second)
	}
	logrus.Info("Token controller has synced with ManagedCluster informer")

	logrus.Info("Waiting for token controller to sync with Secret informer")
	for !secretInformer.HasSynced() {
		time.Sleep(1 * time.Second)
	}
	logrus.Info("Token controller has synced with Secret informer")

	// Start the token manager.
	c.ManageTokens(
		stopCh,
		mcChan,
		secretChan,
		mcInformer,
	)

}

func isConnected(mc *v3.ManagedCluster) bool {
	for _, s := range mc.Status.Conditions {
		if s.Type == v3.ManagedClusterStatusTypeConnected {
			return s.Status == v3.ManagedClusterStatusValueTrue
		}
	}
	logrus.WithField("cluster", mc.Name).Debug("ManagedCluster is not connected")
	return false
}

func retryUpdate[T corev1.Secret | v3.ManagedCluster](rc *retryCalculator, id string, obj T, objChan chan *T, stop <-chan struct{}) {
	updateType := fmt.Sprintf("%T", obj)
	log := logrus.WithField(updateType, id)

	// Check if we should retry this update.
	retry, dur := rc.duration(id)
	if !retry {
		log.Warnf("Giving up on %s", updateType)
		return
	}

	// Schedule a retry.
	go func() {
		log.WithField("wait", dur).Infof("Scheduling retry for failed sync after %.0f seconds", dur.Seconds())
		time.Sleep(dur)

		// Use select to prevent accidentally sending to a closed channel if the controller initiated a shut down
		// while this routine was sleeping.
		select {
		case <-stop:
		default:
			objChan <- &obj
		}
	}()
}

func (c *controller) ManageTokens(stop <-chan struct{}, mcChan chan *v3.ManagedCluster, secretChan chan *corev1.Secret, mcInformer cache.SharedIndexInformer) {
	defer logrus.Info("Token manager shutting down")

	ticker := time.After(*c.reconcilePeriod)
	rc := NewRetryCalculator(*c.baseRetryPeriod, *c.maxRetries)

	// Main loop.
	for {
		select {
		case <-stop:
			return
		case <-ticker:
			logrus.Debug("Reconciling tokens and copying secrets for all clusters")

			// Get all clusters.
			mcs := mcInformer.GetStore().List()

			// Start a new ticker.
			ticker = time.After(*c.reconcilePeriod)

			for _, obj := range mcs {
				mc, ok := obj.(*v3.ManagedCluster)
				if !ok {
					logrus.Warnf("Received unexpected type %T", obj)
					continue
				}
				log := c.loggerForManagedCluster(mc)
				managedClient, err := c.factory.Impersonate(c.impersonationInfo).NewClientSetForApplication(mc.Name)
				if err != nil {
					log.WithError(err).Error("failed to get client for cluster")
					continue
				}

				if err = c.reconcileTokensForCluster(mc, managedClient); err != nil {
					log.WithError(err).Error("failed to reconcile tokens")
				}

				if err = c.reconcileSecretsForCluster(mc, c.secretsToCopy, managedClient); err != nil {
					log.WithError(err).Error("failed to reconcile secrets")
				}
			}

			if c.reportHealth != nil {
				c.reportHealth(&health.HealthReport{Live: true, Ready: true})
			}
		case mc := <-mcChan:
			retry := retryUpdate[v3.ManagedCluster]

			log := c.loggerForManagedCluster(mc)

			// Ensure that the cluster exists before proceeding with the reconciliation.
			// This prevents reconcilation of token and secrets for deleted managed clusters.
			if _, ok, _ := mcInformer.GetStore().Get(mc); !ok {
				log.Info("Manager cluster does not exist")
				continue
			}

			managedClient, err := c.factory.Impersonate(c.impersonationInfo).NewClientSetForApplication(mc.Name)
			if err != nil {
				log.WithError(err).Error("failed to get client for cluster")
				retry(rc, mc.Name, *mc, mcChan, stop)
				continue
			}

			var needRetry bool
			if err = c.reconcileTokensForCluster(mc, managedClient); err != nil {
				log.WithError(err).Error("failed to reconcile tokens for cluster")
				needRetry = true

			}
			if err = c.reconcileSecretsForCluster(mc, c.secretsToCopy, managedClient); err != nil {
				needRetry = true
				log.WithError(err).Error("failed to reconcile secrets for cluster")
			}

			// Use single retry when either or both of them fails.
			if needRetry {
				retry(rc, mc.Name, *mc, mcChan, stop)
			}

		case secret := <-secretChan:
			retry := retryUpdate[corev1.Secret]

			// Get all clusters.
			mcs := mcInformer.GetStore().List()

			for _, obj := range mcs {
				mc, ok := obj.(*v3.ManagedCluster)
				if !ok {
					logrus.Warnf("Received unexpected type %T", obj)
					continue
				}
				log := c.loggerForManagedCluster(mc)

				managedClient, err := c.factory.Impersonate(c.impersonationInfo).NewClientSetForApplication(mc.Name)
				if err != nil {
					log.WithError(err).Error("failed to get client for cluster")
					retry(rc, fmt.Sprintf("%s/%s", secret.Namespace, secret.Name), *secret, secretChan, stop)
					continue
				}

				if err = c.reconcileSecretsForCluster(mc, []corev1.Secret{*secret}, managedClient); err != nil {
					log.WithError(err).Error("failed to reconcile secrets for cluster")
					retry(rc, fmt.Sprintf("%s/%s", secret.Namespace, secret.Name), *secret, secretChan, stop)
				}
			}
		}
	}
}

func NewRetryCalculator(start time.Duration, maxRetries int) *retryCalculator {
	return &retryCalculator{
		startDuration:      start,
		maxRetries:         maxRetries,
		outstandingRetries: map[string]time.Duration{},
		numRetries:         map[string]int{},
	}
}

type retryCalculator struct {
	startDuration      time.Duration
	outstandingRetries map[string]time.Duration
	numRetries         map[string]int
	maxRetries         int
}

// duration returns the next duration to use when retrying the given key.
// after a max number of retries, it will return (false, 0) to indicate that we should give up.
func (r *retryCalculator) duration(key string) (bool, time.Duration) {
	fields := logrus.Fields{"numRetries": r.numRetries[key], "maxRetries": r.maxRetries}
	logrus.WithFields(fields).Debugf("retryCalulator getting duration")
	if r.numRetries[key] >= r.maxRetries {
		// Give up.
		delete(r.numRetries, key)
		delete(r.outstandingRetries, key)
		return false, 0 * time.Second
	}
	r.numRetries[key]++

	if d, ok := r.outstandingRetries[key]; ok {
		// Double the duration, up to a maximum of 1 minute.
		d = d * 2
		if d > 1*time.Minute {
			d = 1 * time.Minute
		}
		r.outstandingRetries[key] = d
		return true, d
	} else {
		// First time we've seen this key.
		d = r.startDuration
		r.outstandingRetries[key] = d
		return true, d
	}
}

func isValid(mc *v3.ManagedCluster) error {
	if mc.Name == "" {
		return fmt.Errorf("Empty cluster name given")
	}

	return nil
}

func (c *controller) loggerForManagedCluster(mc *v3.ManagedCluster) *logrus.Entry {
	name := mc.Name
	if mc.Namespace != "" {
		name = fmt.Sprintf("%s/%s", mc.Namespace, mc.Name)
	}

	logger := logrus.WithField("cluster", name)

	if c.tenant != "" {
		logger = logger.WithField("tenant", c.tenant)
	}

	return logger
}

// reconcileTokens reconciles tokens. This is a hack and should be moved to its own location.
func (c *controller) reconcileTokensForCluster(mc *v3.ManagedCluster, managedClient kubernetes.Interface) error {
	log := c.loggerForManagedCluster(mc)

	if err := isValid(mc); err != nil {
		return err
	} else if !isConnected(mc) {
		log.Debug("ManagedCluster not connected, skipping")
		return nil
	}

	var tokenErrors []error
	for _, user := range c.userInfos {
		log = log.WithField("service", user.Name)

		// First, check if token exists. If it does, we don't need to do anything.
		tokenName := c.tokenNameForService(user.Name)
		if update, err := c.needsUpdate(log, managedClient, mc.Name, tokenName, user); err != nil {
			log.WithError(err).Error("error checking token")
			tokenErrors = append(tokenErrors, err)
			continue
		} else if !update {
			log.Debug("Token does not need to be updated")
			continue
		}

		// Token needs to be created or updated.
		token, err := c.createToken(c.tenant, mc.Name, user)
		if err != nil {
			log.WithError(err).Error("error creating token")
			tokenErrors = append(tokenErrors, err)
			continue
		}

		secret := corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      tokenName,
				Namespace: user.Namespace,
			},
			Data: map[string][]byte{
				"token": token,
			},
		}

		if err = resource.WriteSecretToK8s(managedClient, resource.CopySecret(&secret)); err != nil {
			log.WithError(err).Error("error copying secrets")
			tokenErrors = append(tokenErrors, err)
			continue
		}
		log.WithField("name", secret.Name).Info("Created/updated token secret")
	}

	return errors.Join(tokenErrors...)
}

func (c *controller) reconcileSecretsForCluster(mc *v3.ManagedCluster, secretsToCopy []corev1.Secret, managedClient k8s.ClientSet) error {
	log := c.loggerForManagedCluster(mc)

	if err := isValid(mc); err != nil {
		return err
	} else if !isConnected(mc) {
		log.Debug("ManagedCluster not connected, skipping")
		return nil
	}

	for _, s := range secretsToCopy {
		secret, err := c.managementK8sClient.CoreV1().Secrets(s.Namespace).Get(context.Background(), s.Name, metav1.GetOptions{})
		if err != nil {
			log.WithError(err).Errorf("Error retrieving secret %v in namespace %v", s.Name, s.Namespace)
			return err
		}

		managedOperatorNS, err := utils.FetchOperatorNamespace(managedClient)
		if err != nil {
			log.WithError(err).Error("Unable to fetch managed cluster operator namespace")
			return err
		}

		secret.ObjectMeta.Namespace = managedOperatorNS
		if err = resource.WriteSecretToK8s(managedClient, resource.CopySecret(secret)); err != nil {
			log.WithError(err).Error("Error writing secret to managed cluster")
			return err
		}
		log.WithFields(logrus.Fields{
			"name":      secret.Name,
			"namespace": secret.Namespace,
		}).Debug("Copied secret to managed cluster")
	}
	log.Debug("Successfully copied all secrets")

	return nil
}

func (c *controller) tokenNameForService(service string) string {
	// Secret names should be identified by:
	// - The issuer of the token
	// - The service the token is being created for
	return fmt.Sprintf("%s-%s-token", service, c.issuerName)
}

func (c *controller) needsUpdate(log *logrus.Entry, cs kubernetes.Interface, mcName, tokenName string, user UserInfo) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cm, err := cs.CoreV1().Secrets(user.Namespace).Get(ctx, tokenName, metav1.GetOptions{})
	if err != nil && !k8serrors.IsNotFound(err) {
		// Error querying the token.
		return false, err
	} else if k8serrors.IsNotFound(err) {
		// No token exists.
		return true, nil
	} else {
		// Validate the token to make sure it was signed by us.
		tokenBytes := []byte(cm.Data["token"])
		_, err = jwt.ParseWithClaims(string(tokenBytes), &jwt.RegisteredClaims{}, func(token *jwt.Token) (interface{}, error) {
			return c.privateKey.Public(), nil
		})
		if err != nil {
			// If the token is not signed by us, we should replace it. This covers two cases:
			// - User has manually specified a new invalid token in the secret.
			// - We're using a new cert to sign tokens, invalidating any and all tokens that we
			//   had previously distributed to clients.
			log.WithError(err).Warn("Could not authenticate token")
			return true, nil
		}

		// Parse the token to get its expiry.
		tkn, err := jws.ParseJWT(tokenBytes)
		if err != nil {
			log.WithError(err).Warn("failed to parse token")
			return true, nil
		}
		expiry, exists := tkn.Claims().Expiration()
		if !exists {
			log.Info("token has no expiration data present")
			return true, nil
		}

		// Refresh the token if the time between the expiry and now
		// is less than 2/3 of the total expiry time.
		dur := 2 * c.expiry / 3
		if time.Until(expiry) < dur {
			log.Info("token needs to be refreshed")
			return true, nil
		}

		// Check if the token's subject field is correct for this ManagedCluster.
		subject, exists := tkn.Claims().Subject()
		if !exists {
			log.Debug("token has no subject data present")
			return true, nil
		}

		expectedSubject := GenerateSubjectLinseed(c.tenant, mcName, user.Namespace, user.Name, user.TenantNamespaceOverride)

		if subject != expectedSubject {
			log.Debugf("token subject (%v) does not match expected subject (%v)", subject, expectedSubject)
			return true, nil
		}
	}
	return false, nil
}

func (c *controller) createToken(tenant, cluster string, user UserInfo) ([]byte, error) {
	tokenLifetime := c.expiry
	if tokenLifetime == 0 {
		tokenLifetime = defaultTokenLifetime
	}
	expirationTime := time.Now().Add(tokenLifetime)

	// Subject is a combination of tenantID, clusterID, and service name.
	subj := GenerateSubjectLinseed(tenant, cluster, user.Namespace, user.Name, user.TenantNamespaceOverride)

	claims := &jwt.RegisteredClaims{
		Subject:   subj,
		Issuer:    c.issuer,
		Audience:  jwt.ClaimStrings{c.issuerName},
		IssuedAt:  jwt.NewNumericDate(time.Now()),
		ExpiresAt: jwt.NewNumericDate(expirationTime),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tokenString, err := token.SignedString(c.privateKey)
	if err != nil {
		return nil, err
	}
	return []byte(tokenString), err
}

func ParseSubjectLinseed(subject string) (tenant, cluster, namespace, name string, err error) {
	splits := strings.Split(subject, ":")
	if len(splits) != 4 {
		return "", "", "", "", fmt.Errorf("bad subject")
	}
	return splits[0], splits[1], splits[2], splits[3], nil
}

func GenerateSubjectLinseed(tenant, cluster, namespace, name, namespaceOverride string) string {
	serviceAccountNamespace := namespace
	if namespaceOverride != "" {
		serviceAccountNamespace = namespaceOverride
	}
	return fmt.Sprintf("%s:%s:%s:%s", tenant, cluster, serviceAccountNamespace, name)
}

// ParseClaimsLinseed implements ClaimParser for token claims generated by Linseed.
func ParseClaimsLinseed(claims jwt.Claims) (*user.DefaultInfo, error) {
	reg, ok := claims.(*jwt.RegisteredClaims)
	if !ok {
		logrus.WithField("claims", claims).Warn("given claims were not a RegisteredClaims")
		return nil, fmt.Errorf("invalid claims given")
	}
	_, _, namespace, name, err := ParseSubjectLinseed(reg.Subject)
	if err != nil {
		return nil, err
	}
	return &user.DefaultInfo{Name: fmt.Sprintf("system:serviceaccount:%s:%s", namespace, name)}, nil
}

// newManagedClusterListWatcher returns an implementation of the ListWatch interface capable of being used to
// build an informer based on a controller-runtime client. Using the controller-runtime client allows us to build
// an Informer that works for both namespaced and cluster-scoped ManagedCluster resources regardless of whether
// it is a multi-tenant cluster or not.
func newManagedClusterListWatcher(ctx context.Context, c ctrlclient.WithWatch, namespace string) *cache.ListWatch {
	return &cache.ListWatch{
		ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
			list := &v3.ManagedClusterList{}
			err := c.List(ctx, list, &ctrlclient.ListOptions{Raw: &options, Namespace: namespace})
			return list, err
		},
		WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
			list := &v3.ManagedClusterList{}
			return c.Watch(ctx, list, &ctrlclient.ListOptions{Raw: &options, Namespace: namespace})
		},
	}
}
