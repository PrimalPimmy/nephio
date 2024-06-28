/*
Copyright 2023 The Nephio Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package bootstrapsecret

import (
	"context"
	"fmt"
	"strings"
	"time"

	reconcilerinterface "github.com/nephio-project/nephio/controllers/pkg/reconcilers/reconciler-interface"
	"github.com/nephio-project/nephio/controllers/pkg/resource"
	"github.com/spiffe/go-spiffe/v2/workloadapi"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	"github.com/nephio-project/nephio/controllers/pkg/cluster"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	capiv1beta1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func init() {
	reconcilerinterface.Register("bootstrap-spire", &reconciler{})
}

// const (
// 	clusterNameKey     = "nephio.org/cluster-name"
// 	nephioAppKey       = "nephio.org/app"
// 	remoteNamespaceKey = "nephio.org/remote-namespace"
// 	syncApp            = "tobeinstalledonremotecluster"
// 	bootstrapApp       = "bootstrap"
// )

//+kubebuilder:rbac:groups=cluster.x-k8s.io,resources=clusters,verbs=get;list;watch
//+kubebuilder:rbac:groups=cluster.x-k8s.io,resources=clusters/status,verbs=get

// SetupWithManager sets up the controller with the Manager.
func (r *reconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager, c any) (map[schema.GroupVersionKind]chan event.GenericEvent, error) {
	r.Client = mgr.GetClient()

	return nil, ctrl.NewControllerManagedBy(mgr).
		Named("BootstrapSpireController").
		For(&capiv1beta1.Cluster{}).
		Complete(r)
}

type reconciler struct {
	client.Client
}

// r.List --> gets us cluster name list

func (r *reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// Fetch the Cluster instance

	// List all Cluster instances
	// clusterList := &capiv1beta1.ClusterList{}
	// err := r.List(ctx, clusterList)
	// if err != nil {
	// 	log.Error(err, "unable to list Clusters")
	// 	return reconcile.Result{}, err
	// }

	cl := &capiv1beta1.Cluster{}
	err := r.Get(ctx, req.NamespacedName, cl)
	if err != nil {
		if client.IgnoreNotFound(err) != nil {
			log.Error(err, "unable to fetch Cluster")
		}
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	// Add your reconciliation logic here
	log.Info("Reconciling Cluster", "cluster", cl.Name)

	// Fetch the ConfigMap from the current cluster
	configMapName := types.NamespacedName{Name: "spire-bundle", Namespace: "spire"}
	configMap := &corev1.ConfigMap{}
	err = r.Get(ctx, configMapName, configMap)
	if err != nil {
		log.Error(err, "unable to fetch ConfigMap")
		return reconcile.Result{}, err
	}

	secrets := &corev1.SecretList{}
	if err := r.List(ctx, secrets); err != nil {
		msg := "cannot list secrets"
		log.Error(err, msg)
		return ctrl.Result{}, errors.Wrap(err, msg)
	}

	found := false
	for _, secret := range secrets.Items {
		if strings.Contains(secret.GetName(), cl.Name) {
			secret := secret // required to prevent gosec warning: G601 (CWE-118): Implicit memory aliasing in for loop
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

				remoteNamespace := configMap.Namespace
				// if rns, ok := configMap.GetAnnotations()[remoteNamespaceKey]; ok {
				// 	remoteNamespace = rns
				// }
				// check if the remote namespace exists, if not retry
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

				newcr := configMap.DeepCopy()

				newcr.ResourceVersion = ""
				newcr.UID = ""
				newcr.Namespace = remoteNamespace
				log.Info("secret info", "secret", newcr.Annotations)
				if err := clusterClient.Apply(ctx, newcr); err != nil {
					msg := fmt.Sprintf("cannot apply secret to cluster %s", cl.Name)
					log.Error(err, msg)
					return ctrl.Result{}, errors.Wrap(err, msg)
				}
			}
		}
		if found {
			// speeds up the loop
			break
		}
	}

	// Example: Update the status if necessary

	return reconcile.Result{}, nil
}

func fetchJWTSVID() (string, error) {
	_, err := workloadapi.New(context.Background(), workloadapi.WithAddr("unix:///run/spire/sockets/agent.sock"))
	return "", err
}
