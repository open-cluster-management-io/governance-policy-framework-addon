// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package e2e

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"open-cluster-management.io/governance-policy-propagator/test/utils"
)

const (
	case15PolicyName             string = "case15-test-policy"
	case15PolicyYaml             string = "../resources/case15_template_cleanup/case15-test-policy.yaml"
	case15PolicyYamlPatchRename  string = "../resources/case15_template_cleanup/case15-test-policy-patch-rename.yaml"
	case15PolicyYamlPatchDelete  string = "../resources/case15_template_cleanup/case15-test-policy-patch-delete.yaml"
	case15ConfigPolicyName       string = "case15-config-policy"
	case15ConfigPolicyRenamed    string = "case15-config-policy-renamed"
	case15ConfigPolicyNameStable string = "case15-config-policy-stable"
)

var _ = Describe("Test template sync", func() {
	BeforeEach(func() {
		By("Creating a policy on the hub in ns:" + clusterNamespaceOnHub)
		_, err := kubectlHub("apply", "-f", case15PolicyYaml, "-n", clusterNamespaceOnHub)
		Expect(err).Should(BeNil())
		plc := utils.GetWithTimeout(clientManagedDynamic, gvrPolicy, case15PolicyName, clusterNamespace, true,
			defaultTimeoutSeconds)
		Expect(plc).NotTo(BeNil())
	})
	AfterEach(func() {
		By("Deleting a policy on the hub in ns:" + clusterNamespaceOnHub)
		_, err := kubectlHub("delete", "-f", case15PolicyYaml, "-n", clusterNamespaceOnHub, "--ignore-not-found=true")
		Expect(err).To(BeNil())
		opt := metav1.ListOptions{}
		utils.ListWithTimeout(clientManagedDynamic, gvrPolicy, opt, 0, true, defaultTimeoutSeconds)
	})
	It("should create policy template on managed cluster and remove it on rename", func() {
		By("Checking the configpolicy CRs")
		yamlPlc := utils.ParseYaml("../resources/case15_template_cleanup/case15-config-policy-rename.yaml")
		Eventually(func() interface{} {
			trustedPlc := utils.GetWithTimeout(clientManagedDynamic, gvrConfigurationPolicy,
				case15ConfigPolicyName, clusterNamespace, true, defaultTimeoutSeconds)

			return trustedPlc.Object["spec"]
		}, defaultTimeoutSeconds, 1).Should(utils.SemanticEqual(yamlPlc.Object["spec"]))
		yamlStablePlc := utils.ParseYaml("../resources/case15_template_cleanup/case15-config-policy-stable.yaml")
		Eventually(func() interface{} {
			trustedPlc := utils.GetWithTimeout(clientManagedDynamic, gvrConfigurationPolicy,
				case15ConfigPolicyNameStable, clusterNamespace, true, defaultTimeoutSeconds)

			return trustedPlc.Object["spec"]
		}, defaultTimeoutSeconds, 1).Should(utils.SemanticEqual(yamlStablePlc.Object["spec"]))

		By("Renaming one of the configurationpolicies")
		_, err := kubectlHub("apply", "-f", case15PolicyYamlPatchRename, "-n", clusterNamespaceOnHub)
		Expect(err).Should(BeNil())
		plc := utils.GetWithTimeout(clientManagedDynamic, gvrPolicy, case15PolicyName, clusterNamespace, true,
			defaultTimeoutSeconds)
		Expect(plc).NotTo(BeNil())

		By("Verifying the changed config policy has been deleted and the stable one still exists")
		cfgplc := utils.GetWithTimeout(clientManagedDynamic, gvrConfigurationPolicy, case15ConfigPolicyNameStable,
			clusterNamespace, true, defaultTimeoutSeconds)
		Expect(cfgplc).NotTo(BeNil())
		cfgplc = utils.GetWithTimeout(clientManagedDynamic, gvrConfigurationPolicy, case15ConfigPolicyRenamed,
			clusterNamespace, true, defaultTimeoutSeconds)
		Expect(cfgplc).NotTo(BeNil())
		cfgplc = utils.GetWithTimeout(clientManagedDynamic, gvrConfigurationPolicy, case15ConfigPolicyName,
			clusterNamespace, false, defaultTimeoutSeconds)
		Expect(cfgplc).To(BeNil())

		By("Verifying the spec is correct for both existing CRs")
		yamlPlc = utils.ParseYaml("../resources/case15_template_cleanup/case15-config-policy-rename.yaml")
		Eventually(func() interface{} {
			trustedPlc := utils.GetWithTimeout(clientManagedDynamic, gvrConfigurationPolicy,
				case15ConfigPolicyRenamed, clusterNamespace, true, defaultTimeoutSeconds)

			return trustedPlc.Object["spec"]
		}, defaultTimeoutSeconds, 1).Should(utils.SemanticEqual(yamlPlc.Object["spec"]))
		yamlStablePlc = utils.ParseYaml("../resources/case15_template_cleanup/case15-config-policy-stable.yaml")
		Eventually(func() interface{} {
			trustedPlc := utils.GetWithTimeout(clientManagedDynamic, gvrConfigurationPolicy,
				case15ConfigPolicyNameStable, clusterNamespace, true, defaultTimeoutSeconds)

			return trustedPlc.Object["spec"]
		}, defaultTimeoutSeconds, 1).Should(utils.SemanticEqual(yamlStablePlc.Object["spec"]))

		By("Removing one of the configurationpolicies")
		_, err = kubectlHub("apply", "-f", case15PolicyYamlPatchDelete, "-n", clusterNamespaceOnHub)
		Expect(err).Should(BeNil())
		plc = utils.GetWithTimeout(clientManagedDynamic, gvrPolicy, case15PolicyName, clusterNamespace, true,
			defaultTimeoutSeconds)
		Expect(plc).NotTo(BeNil())

		By("Verifying the removed config policy has been deleted and the stable one still exists")
		cfgplc = utils.GetWithTimeout(clientManagedDynamic, gvrConfigurationPolicy, case15ConfigPolicyNameStable,
			clusterNamespace, true, defaultTimeoutSeconds)
		Expect(cfgplc).NotTo(BeNil())
		cfgplc = utils.GetWithTimeout(clientManagedDynamic, gvrConfigurationPolicy, case15ConfigPolicyRenamed,
			clusterNamespace, false, defaultTimeoutSeconds)
		Expect(cfgplc).To(BeNil())
	})
})
