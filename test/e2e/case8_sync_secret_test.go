// Copyright (c) 2021 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package e2e

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"open-cluster-management.io/governance-policy-propagator/test/utils"

	"open-cluster-management.io/governance-policy-framework-addon/controllers/secretsync"
)

var _ = Describe("Test secret sync", func() {
	const (
		case8SecretYAML          = "../resources/case8_sync_secret/secret.yaml"
		case8UnrelatedSecretYAML = "../resources/case8_sync_secret/unrelated_secret.yaml"
	)

	AfterEach(func() {
		By("Deleting the test secrets on the Hub")
		_, _ = kubectlHub("delete", "-f", case8SecretYAML, "-n", clusterNamespaceOnHub)
		_, _ = kubectlHub("delete", "-f", case8UnrelatedSecretYAML, "-n", clusterNamespaceOnHub)
	})

	It("should sync the secret to the managed cluster when created on the hub", func() {
		_, _ = kubectlHub("apply", "-f", case8SecretYAML, "-n", clusterNamespaceOnHub)
		managedSecret := utils.GetWithTimeout(
			clientManagedDynamic,
			gvrSecret,
			secretsync.SecretName,
			clusterNamespace,
			true,
			defaultTimeoutSeconds,
		)
		Expect(managedSecret).NotTo(BeNil())
	})

	It("should not sync the unrelated secret to the managed cluster when created on the hub", func() {
		_, _ = kubectlHub("apply", "-f", case8UnrelatedSecretYAML, "-n", clusterNamespaceOnHub)
		// Sleep 5 seconds to ensure the secret isn't synced.
		time.Sleep(5 * time.Second)
		managedSecret := utils.GetWithTimeout(
			clientManagedDynamic,
			gvrSecret,
			"not-the-policy-encryption-key",
			clusterNamespace,
			false,
			defaultTimeoutSeconds,
		)
		Expect(managedSecret).To(BeNil())
	})
})
