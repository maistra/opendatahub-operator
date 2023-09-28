package cluster

import (
	"context"
	corev1 "k8s.io/api/core/v1"
	authv1 "k8s.io/api/rbac/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// CreateNamespace creates new namespace of a given name.
func CreateNamespace(cli client.Client, namespace string) error {
	desiredNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: namespace,
			Labels: map[string]string{
				"opendatahub.io/generated-namespace": "true",
			},
		},
	}

	foundNamespace := &corev1.Namespace{}
	err := cli.Get(context.TODO(), client.ObjectKey{Name: namespace}, foundNamespace)
	if err != nil {
		if apierrs.IsNotFound(err) {
			err = cli.Create(context.TODO(), desiredNamespace)
			if err != nil && !apierrs.IsAlreadyExists(err) {
				return err
			}
		} else {
			return err
		}
	}
	return nil
}

// UpdatePodSecurityRolebinding update default rolebinding which is created in applications namespace by manifests
// being used by different components.
func UpdatePodSecurityRolebinding(cli client.Client, namespace string, serviceAccounts ...string) error {
	foundRoleBinding := &authv1.RoleBinding{}
	err := cli.Get(context.TODO(), client.ObjectKey{Name: namespace, Namespace: namespace}, foundRoleBinding)
	if err != nil {
		return err
	}

	for _, sa := range serviceAccounts {
		// Append serviceAccount if not added already
		if !subjectExistInRoleBinding(namespace, sa, foundRoleBinding.Subjects...) {
			foundRoleBinding.Subjects = append(foundRoleBinding.Subjects, authv1.Subject{
				Kind:      authv1.ServiceAccountKind,
				Name:      sa,
				Namespace: namespace})
		}
	}

	return cli.Update(context.TODO(), foundRoleBinding)
}

// Internal function used by UpdatePodSecurityRolebinding()
// Return whether Rolebinding matching service account and namespace exists or not
func subjectExistInRoleBinding(namespace, serviceAccountName string, subjects ...authv1.Subject) bool {
	for _, subject := range subjects {
		if subject.Name == serviceAccountName && subject.Namespace == namespace {
			return true
		}
	}

	return false
}

// CreateSecret creates secrets required by dashboard component in downstream.
func CreateSecret(cli client.Client, name, namespace string) error {
	desiredSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Type: corev1.SecretTypeOpaque,
	}

	foundSecret := &corev1.Secret{}
	err := cli.Get(context.TODO(), client.ObjectKey{Name: name, Namespace: namespace}, foundSecret)
	if err != nil {
		if apierrs.IsNotFound(err) {
			err = cli.Create(context.TODO(), desiredSecret)
			if err != nil && !apierrs.IsAlreadyExists(err) {
				return err
			}
		} else {
			return err
		}
	}
	return nil
}
