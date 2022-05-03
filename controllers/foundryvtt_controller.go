/*
Copyright 2022.

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

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	vttv1 "github.com/droideck/foundryvtt-operator/api/v1"
)

// FoundryvttReconciler reconciles a Foundryvtt object
type FoundryvttReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=vtt.spichugin.dev,resources=foundryvtts,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=vtt.spichugin.dev,resources=foundryvtts/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=vtt.spichugin.dev,resources=foundryvtts/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Foundryvtt object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.10.0/pkg/reconcile
func (r *FoundryvttReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("foundryvtt", req.NamespacedName)

	// Fetch the Foundryvtt instance
	foundryvtt := &cachev1alpha1.Foundryvtt{}
	err := r.Get(ctx, req.NamespacedName, foundryvtt)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			log.Info("Foundryvtt resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		log.Error(err, "Failed to get Foundryvtt")
		return ctrl.Result{}, err
	}

	// Check if the deployment already exists, if not create a new one
	found := &appsv1.Deployment{}
	err = r.Get(ctx, types.NamespacedName{Name: foundryvtt.Name, Namespace: foundryvtt.Namespace}, found)
	if err != nil && errors.IsNotFound(err) {
		// Define a new deployment
		dep := r.deploymentForFoundryvtt(foundryvtt)
		log.Info("Creating a new Deployment", "Deployment.Namespace", dep.Namespace, "Deployment.Name", dep.Name)
		err = r.Create(ctx, dep)
		if err != nil {
			log.Error(err, "Failed to create new Deployment", "Deployment.Namespace", dep.Namespace, "Deployment.Name", dep.Name)
			return ctrl.Result{}, err
		}
		// Deployment created successfully - return and requeue
		return ctrl.Result{Requeue: true}, nil
	} else if err != nil {
		log.Error(err, "Failed to get Deployment")
		return ctrl.Result{}, err
	}

	// Ensure the deployment size is the same as the spec
	size := foundryvtt.Spec.Size
	if *found.Spec.Replicas != size {
		found.Spec.Replicas = &size
		err = r.Update(ctx, found)
		if err != nil {
			log.Error(err, "Failed to update Deployment", "Deployment.Namespace", found.Namespace, "Deployment.Name", found.Name)
			return ctrl.Result{}, err
		}
		// Spec updated - return and requeue
		return ctrl.Result{Requeue: true}, nil
	}

	// Update the Foundryvtt status with the pod names
	// List the pods for this foundryvtt's deployment
	podList := &corev1.PodList{}
	listOpts := []client.ListOption{
		client.InNamespace(foundryvtt.Namespace),
		client.MatchingLabels(labelsForFoundryvtt(foundryvtt.Name)),
	}
	if err = r.List(ctx, podList, listOpts...); err != nil {
		log.Error(err, "Failed to list pods", "Foundryvtt.Namespace", foundryvtt.Namespace, "Foundryvtt.Name", foundryvtt.Name)
		return ctrl.Result{}, err
	}
	podNames := getPodNames(podList.Items)

	// Update status.Nodes if needed
	if !reflect.DeepEqual(podNames, foundryvtt.Status.Nodes) {
		foundryvtt.Status.Nodes = podNames
		err := r.Status().Update(ctx, foundryvtt)
		if err != nil {
			log.Error(err, "Failed to update Foundryvtt status")
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

// deploymentForFoundryvtt returns a foundryvtt Deployment object
func (r *FoundryvttReconciler) deploymentForFoundryvtt(m *cachev1alpha1.Foundryvtt) *appsv1.Deployment {
	ls := labelsForFoundryvtt(m.Name)
	replicas := m.Spec.Size

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      m.Name,
			Namespace: m.Namespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: ls,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: ls,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Image:   "foundryvtt:1.4.36-alpine",
						Name:    "foundryvtt",
						Command: []string{"foundryvtt", "-m=64", "-o", "modern", "-v"},
						Ports: []corev1.ContainerPort{{
							ContainerPort: 11211,
							Name:          "foundryvtt",
						}},
					}},
				},
			},
		},
	}
	// Set Foundryvtt instance as the owner and controller
	ctrl.SetControllerReference(m, dep, r.Scheme)
	return dep
}

// labelsForFoundryvtt returns the labels for selecting the resources
// belonging to the given foundryvtt CR name.
func labelsForFoundryvtt(name string) map[string]string {
	return map[string]string{"app": "foundryvtt", "foundryvtt_cr": name}
}

// getPodNames returns the pod names of the array of pods passed in
func getPodNames(pods []corev1.Pod) []string {
	var podNames []string
	for _, pod := range pods {
		podNames = append(podNames, pod.Name)
	}
	return podNames
}


// SetupWithManager sets up the controller with the Manager.
func (r *FoundryvttReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&vttv1.Foundryvtt{}).
		Complete(r)
}
