package bootstrapsecret

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"time"

	"github.com/nephio-project/nephio/controllers/pkg/cluster"
	reconcilerinterface "github.com/nephio-project/nephio/controllers/pkg/reconcilers/reconciler-interface"
	"github.com/nephio-project/nephio/controllers/pkg/resource"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func init() {
	reconcilerinterface.Register("spire-bootstrapsecrets", &reconciler{})
}

const (
	clusterNameKey     = "nephio.org/cluster-name"
	remoteNamespaceKey = "nephio.org/remote-namespace"
  syncApp            = "tobeinstalledonremotecluster"
	bootstrapApp       = "bootstrap"
)

//+kubebuilder:rbac:groups="*",resources=secrets,verbs=get;list;watch
//+kubebuilder:rbac:groups=cluster.x-k8s.io,resources=clusters,verbs=get;list;watch
//+kubebuilder:rbac:groups=cluster.x-k8s.io,resources=clusters/status,verbs=get

// SetupWithManager sets up the controller with the Manager.
func (r *reconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager, c any) (map[schema.GroupVersionKind]chan event.GenericEvent, error) {
	r.Client = mgr.GetClient()

	return nil, ctrl.NewControllerManagedBy(mgr).
		Named("BootstrapSecretController").
		For(&corev1.Secret{}).
		Complete(r)
}

type reconciler struct {
	client.Client
}

func (r *reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	cr := &corev1.Secret{}
	if err := r.Get(ctx, req.NamespacedName, cr); err != nil {
		if resource.IgnoreNotFound(err) != nil {
			msg := "cannot get resource"
			log.Error(err, msg)
			return ctrl.Result{}, errors.Wrap(resource.IgnoreNotFound(err), msg)
		}
		return reconcile.Result{}, nil
	}

	if resource.WasDeleted(cr) {
		return reconcile.Result{}, nil
	}

		if cr.GetAnnotations()[nephioAppKey] == syncApp &&
		cr.GetAnnotations()[clusterNameKey] != "" &&
		cr.GetAnnotations()[clusterNameKey] != "mgmt" {
		log.Info("reconcile secret")

		clusterName := cr.GetAnnotations()[clusterNameKey]
		clusterNames := strings.Split(clusterName, ",")

		for _, clusterName := range clusterNames {
			secrets := &corev1.SecretList{}
			if err := r.List(ctx, secrets); err != nil {
				msg := "cannot list secrets"
				log.Error(err, msg)
				return ctrl.Result{}, errors.Wrap(err, msg)
			}
			found := false
			for _, secret := range secrets.Items {
				if strings.Contains(secret.GetName(), clusterName) {
					secret := secret
					clusterClient, ok := cluster.Cluster{Client: r.Client}.GetClusterClient(&secret)
					if ok {
						found = true
						clusterClient, ready, err := clusterClient.GetClusterClient(ctx)
						if err != nil {
							msg := "cannot get clusterClient"
							log.Error(err, msg)
							return ctrl.Result{RequeueAfter: 30 * time.Second}, errors.Wrap(err, msg)
						}
						if !ready {
							log.Info("cluster not ready")
							return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
						}

						remoteNamespace := cr.Namespace
						if rns, ok := cr.GetAnnotations()[remoteNamespaceKey]; ok {
							remoteNamespace = rns
						}
						ns := &corev1.Namespace{}
						if err = clusterClient.Get(ctx, types.NamespacedName{Name: remoteNamespace}, ns); err != nil {
							if resource.IgnoreNotFound(err) != nil {
								msg := fmt.Sprintf("cannot get namespace: %s", remoteNamespace)
								log.Error(err, msg)
								return ctrl.Result{RequeueAfter: 30 * time.Second}, errors.Wrap(err, msg)
							}
							msg := fmt.Sprintf("namespace: %s, does not exist, retry...", remoteNamespace)
							log.Info(msg)
							return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
						}

						bootstrapCrt, err := generateBootstrapCrt()
						if err != nil {
							msg := "cannot generate bootstrap.crt"
							log.Error(err, msg)
							return ctrl.Result{}, errors.Wrap(err, msg)
						}

						newSecret := &corev1.Secret{
							ObjectMeta: cr.ObjectMeta,
							Data: map[string][]byte{
								"bootstrap.crt": bootstrapCrt,
							},
						}
						newSecret.Namespace = remoteNamespace
						newSecret.ResourceVersion = ""
						newSecret.UID = ""

						if err := clusterClient.Apply(ctx, newSecret); err != nil {
							msg := fmt.Sprintf("cannot apply secret to cluster %s", clusterName)
							log.Error(err, msg)
							return ctrl.Result{}, errors.Wrap(err, msg)
						}
					}
				}
				if found {
					break
				}
			}
			if !found {
				log.Info("cluster client not found, retry...")
				return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
			}
		}
	}
	return ctrl.Result{}, nil
}

func generateBootstrapCrt() ([]byte, error) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}

	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"Nephio"},
		},
		NotBefore: time.Now(),
		NotAfter:  time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:  x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return nil, err
	}

	certOut := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	return certOut, nil
}
