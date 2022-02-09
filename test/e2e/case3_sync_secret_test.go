// Copyright (c) 2021 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package e2e

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"open-cluster-management.io/governance-policy-propagator/test/utils"

	"open-cluster-management.io/governance-policy-spec-sync/controllers/secretsync"
)

const (
	case3SecretYAML          = "../resources/case3_sync_secret/secret.yaml"
	case3UnrelatedSecretYAML = "../resources/case3_sync_secret/unrelated_secret.yaml"
)

var _ = Describe("Test spec sync", func() {
	AfterEach(func() {
		By("Deleting the test secrets on the Hub")
		_, _ = utils.KubectlWithOutput(
			"delete", "-f", case3SecretYAML, "-n", testNamespace, "--kubeconfig=../../kubeconfig_hub",
		)
		_, _ = utils.KubectlWithOutput(
			"delete", "-f", case3UnrelatedSecretYAML, "-n", testNamespace, "--kubeconfig=../../kubeconfig_hub",
		)
	})

	It("should sync the secret to the managed cluster when created on the hub", func() {
		_, _ = utils.KubectlWithOutput(
			"apply", "-f", case3SecretYAML, "-n", testNamespace, "--kubeconfig=../../kubeconfig_hub",
		)
		managedSecret := utils.GetWithTimeout(
			clientManagedDynamic,
			gvrSecret,
			secretsync.SecretName,
			testNamespace,
			true,
			defaultTimeoutSeconds,
		)
		Expect(managedSecret).NotTo(BeNil())
	})

	It("should not sync the unrelated secret to the managed cluster when created on the hub", func() {
		_, _ = utils.KubectlWithOutput(
			"apply", "-f", case3UnrelatedSecretYAML, "-n", testNamespace, "--kubeconfig=../../kubeconfig_hub",
		)
		// Sleep 5 seconds to ensure the secret isn't synced.
		time.Sleep(5 * time.Second)
		managedSecret := utils.GetWithTimeout(
			clientManagedDynamic,
			gvrSecret,
			"not-the-policy-encryption-key",
			testNamespace,
			false,
			defaultTimeoutSeconds,
		)
		Expect(managedSecret).To(BeNil())
	})
})
