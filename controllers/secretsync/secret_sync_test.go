// Copyright (c) 2021 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package secretsync

import (
	"context"
	"crypto/rand"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	clusterName = "managed"
	keySize     = 256
)

func getTestSecret() *corev1.Secret {
	// Generate an AES-256 key and stored it as a Secret on the Hub.
	key := make([]byte, keySize/8)
	_, err := rand.Read(key)
	Expect(err).To(BeNil())

	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      SecretName,
			Namespace: clusterName,
		},
		Data: map[string][]byte{
			"key": key,
		},
	}
}

func TestReconcileSecretHubOnly(t *testing.T) {
	RegisterFailHandler(Fail)

	encryptionSecret := getTestSecret()
	hubClient := fake.NewClientBuilder().WithObjects(encryptionSecret).Build()
	managedClient := fake.NewClientBuilder().Build()

	r := SecretReconciler{
		Client: hubClient, ManagedClient: managedClient, Scheme: scheme.Scheme, TargetNamespace: clusterName,
	}
	request := reconcile.Request{
		NamespacedName: types.NamespacedName{Name: SecretName, Namespace: clusterName},
	}
	_, err := r.Reconcile(context.TODO(), request)
	Expect(err).To(BeNil())

	// Verify that the Secret was synced to the managed cluster by the Reconciler.
	managedEncryptionSecret := &corev1.Secret{}
	err = managedClient.Get(context.TODO(), request.NamespacedName, managedEncryptionSecret)
	Expect(err).To(BeNil())
	Expect(len(managedEncryptionSecret.Data["key"])).To(Equal(keySize / 8))
}

func TestReconcileSecretHubOnlyDiffTargetNS(t *testing.T) {
	RegisterFailHandler(Fail)

	encryptionSecret := getTestSecret()
	targetNamespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "other-ns"}}
	hubClient := fake.NewClientBuilder().WithObjects(encryptionSecret, targetNamespace).Build()
	managedClient := fake.NewClientBuilder().Build()

	r := SecretReconciler{
		Client: hubClient, ManagedClient: managedClient, Scheme: scheme.Scheme, TargetNamespace: "other-ns",
	}
	request := reconcile.Request{
		NamespacedName: types.NamespacedName{Name: SecretName, Namespace: clusterName},
	}
	_, err := r.Reconcile(context.TODO(), request)
	Expect(err).To(BeNil())

	// Verify that the Secret was synced to the managed cluster by the Reconciler.
	managedEncryptionSecret := &corev1.Secret{}
	err = managedClient.Get(
		context.TODO(), types.NamespacedName{Name: SecretName, Namespace: "other-ns"}, managedEncryptionSecret,
	)
	Expect(err).To(BeNil())
	Expect(len(managedEncryptionSecret.Data["key"])).To(Equal(keySize / 8))
}

func TestReconcileSecretAlreadySynced(t *testing.T) {
	RegisterFailHandler(Fail)

	encryptionSecret := getTestSecret()
	hubClient := fake.NewClientBuilder().WithObjects(encryptionSecret).Build()
	managedClient := fake.NewClientBuilder().WithObjects(encryptionSecret).Build()

	managedEncryptionSecret := &corev1.Secret{}
	request := reconcile.Request{
		NamespacedName: types.NamespacedName{Name: SecretName, Namespace: clusterName},
	}
	err := managedClient.Get(context.TODO(), request.NamespacedName, managedEncryptionSecret)
	Expect(err).To(BeNil())

	version := managedEncryptionSecret.ObjectMeta.ResourceVersion

	r := SecretReconciler{
		Client: hubClient, ManagedClient: managedClient, Scheme: scheme.Scheme, TargetNamespace: clusterName,
	}
	_, err = r.Reconcile(context.TODO(), request)
	Expect(err).To(BeNil())

	// Verify that the Secret was not modified by the Reconciler.
	managedEncryptionSecret = &corev1.Secret{}
	err = managedClient.Get(context.TODO(), request.NamespacedName, managedEncryptionSecret)
	Expect(err).To(BeNil())
	Expect(managedEncryptionSecret.ResourceVersion).To(Equal(version))
}

func TestReconcileSecretMismatch(t *testing.T) {
	RegisterFailHandler(Fail)

	hubEncryptionSecret := getTestSecret()
	hubClient := fake.NewClientBuilder().WithObjects(hubEncryptionSecret).Build()
	managedEncryptionSecret := getTestSecret()
	managedEncryptionSecret.Data["key"] = []byte{byte('A')}
	managedClient := fake.NewClientBuilder().WithObjects(managedEncryptionSecret).Build()
	request := reconcile.Request{
		NamespacedName: types.NamespacedName{Name: SecretName, Namespace: clusterName},
	}

	r := SecretReconciler{
		Client: hubClient, ManagedClient: managedClient, Scheme: scheme.Scheme, TargetNamespace: clusterName,
	}
	_, err := r.Reconcile(context.TODO(), request)
	Expect(err).To(BeNil())

	// Verify that the Secret was updated by the Reconciler.
	managedEncryptionSecret = &corev1.Secret{}
	err = managedClient.Get(context.TODO(), request.NamespacedName, managedEncryptionSecret)
	Expect(err).To(BeNil())
	Expect(len(managedEncryptionSecret.Data["key"])).To(Equal(keySize / 8))
}

func TestReconcileSecretDeletedOnHub(t *testing.T) {
	RegisterFailHandler(Fail)

	encryptionSecret := getTestSecret()
	hubClient := fake.NewClientBuilder().Build()
	managedClient := fake.NewClientBuilder().WithObjects(encryptionSecret).Build()

	r := SecretReconciler{
		Client: hubClient, ManagedClient: managedClient, Scheme: scheme.Scheme, TargetNamespace: clusterName,
	}
	request := reconcile.Request{
		NamespacedName: types.NamespacedName{Name: SecretName, Namespace: clusterName},
	}
	_, err := r.Reconcile(context.TODO(), request)
	Expect(err).To(BeNil())

	// Verify that the Secret was deleted on the managed cluster by the Reconciler.
	managedEncryptionSecret := &corev1.Secret{}
	err = managedClient.Get(context.TODO(), request.NamespacedName, managedEncryptionSecret)
	Expect(errors.IsNotFound(err)).To(BeTrue())
}

// The tested code should occur in production because of the field selector set on the watch, but
// the code should still account for it.
func TestReconcileInvalidSecretName(t *testing.T) {
	RegisterFailHandler(Fail)

	encryptionSecret := getTestSecret()
	encryptionSecret.ObjectMeta.Name = "not-the-secret"
	hubClient := fake.NewClientBuilder().WithObjects(encryptionSecret).Build()
	managedClient := fake.NewClientBuilder().Build()

	r := SecretReconciler{
		Client: hubClient, ManagedClient: managedClient, Scheme: scheme.Scheme, TargetNamespace: clusterName,
	}
	request := reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "not-the-secret", Namespace: clusterName},
	}
	_, err := r.Reconcile(context.TODO(), request)
	Expect(err).To(BeNil())

	// Verify that the Secret was not synced to the managed cluster by the Reconciler.
	managedEncryptionSecret := &corev1.Secret{}
	err = managedClient.Get(context.TODO(), request.NamespacedName, managedEncryptionSecret)
	Expect(errors.IsNotFound(err)).To(BeTrue())
}
