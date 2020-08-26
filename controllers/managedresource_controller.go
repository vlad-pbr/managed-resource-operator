/*
Copyright 2020 Vladislav Poberezhny.

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

package controllers

import (
	"context"
	"time"

	"github.com/go-logr/logr"
	"github.com/prometheus/common/log"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	paasv1beta1 "operator/api/v1beta1"

	"operator/pkg/utils"
)

// ManagedResourceReconciler reconciles a ManagedResource object
type ManagedResourceReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=paas.il,resources=managedresources,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=paas.il,resources=managedresources/status,verbs=get;update;patch

func finishReconciliation(result ctrl.Result, err error, managedResource *paasv1beta1.ManagedResource, r *ManagedResourceReconciler) (ctrl.Result, error) {
	if err != nil {
		(*managedResource).Status.State = utils.StateError
		(*managedResource).Status.Info = err.Error()
	} else {
		(*managedResource).Status.State = utils.StateManaged
		(*managedResource).Status.Info = "Managing resource"
		(*managedResource).Status.LastSuccessfulUpdate = time.Now().Format(time.RFC3339)
	}

	if err := r.Status().Update(context.Background(), managedResource); err != nil {
		log.Error(err)
		return ctrl.Result{}, err
	}

	return result, nil
}

// Reconcile reconciles a received resource
func (r *ManagedResourceReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	_ = r.Log.WithValues("managedresource", req.NamespacedName)

	// Get managed resource k8s object
	managedResource := &paasv1beta1.ManagedResource{}
	if err := r.Get(ctx, req.NamespacedName, managedResource); err != nil {
		log.Error(err)
		return finishReconciliation(ctrl.Result{}, err, managedResource, r)
	}

	// Get managed resource bytes
	managedResourceBytes, err := utils.GetManagedResourceBytes(managedResource.Spec.Source)
	if err != nil {
		log.Error(err)
		return finishReconciliation(ctrl.Result{}, err, managedResource, r)
	}

	// Decode managed resource bytes to runtime object
	managedObject, _, err := utils.ObjectSerializer.Decode(managedResourceBytes, nil, &unstructured.Unstructured{})
	if err != nil {
		log.Error(err)
		return finishReconciliation(ctrl.Result{}, err, managedResource, r)
	}

	// Get managed resource object key
	managedObjectKey, err := client.ObjectKeyFromObject(managedObject)
	if err != nil {
		log.Error(err)
		return finishReconciliation(ctrl.Result{}, err, managedResource, r)
	}

	// Try getting object from cluster
	clusterObject := managedObject.DeepCopyObject()
	if err = r.Client.Get(ctx, managedObjectKey, clusterObject); err != nil {

		if apierrors.IsNotFound(err) {

			// Create the managed resource
			if err := r.Client.Create(ctx, managedObject); err != nil {
				log.Error(err)
				return finishReconciliation(ctrl.Result{}, err, managedResource, r)
			}

		} else {
			log.Error(err)
			return finishReconciliation(ctrl.Result{}, err, managedResource, r)
		}

	} else {

		// Insert .metadata.resourceVersion field into managed object
		if err := utils.CopyResourceVersion(clusterObject, &managedObject); err != nil {
			log.Error(err)
			return finishReconciliation(ctrl.Result{}, err, managedResource, r)
		}

		// Update the managed resource
		if err := r.Client.Update(ctx, managedObject); err != nil {
			log.Error(err)
			return finishReconciliation(ctrl.Result{}, err, managedResource, r)
		}
	}

	return finishReconciliation(ctrl.Result{}, nil, managedResource, r)
}

// SetupWithManager registers controller with the manager
func (r *ManagedResourceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&paasv1beta1.ManagedResource{}).
		Complete(r)
}
