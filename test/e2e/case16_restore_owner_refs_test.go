// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package e2e

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"open-cluster-management.io/governance-policy-propagator/test/utils"
)

var _ = Describe("Test owner reference recovery", func() {
	const (
		case16PolicyName            string = "case16-test-policy"
		case16PolicyYaml            string = "../resources/case16_restore_owner_refs/case16-test-policy.yaml"
		case16ConfigPolicyName      string = "case16-config-policy"
		case16PatchConfigPolicyYaml string = "../resources/case16_restore_owner_refs/case16-patch-configpolicy.yaml"
	)

	BeforeEach(func() {
		By("Creating a policy on the hub in ns:" + clusterNamespaceOnHub)
		_, err := kubectlHub("apply", "-f", case16PolicyYaml, "-n", clusterNamespaceOnHub)
		Expect(err).Should(BeNil())
		plc := utils.GetWithTimeout(clientManagedDynamic, gvrPolicy, case16PolicyName, clusterNamespace, true,
			defaultTimeoutSeconds)
		Expect(plc).NotTo(BeNil())
	})
	AfterEach(func() {
		By("Deleting a policy on the hub in ns:" + clusterNamespaceOnHub)
		_, err := kubectlHub("delete", "-f", case16PolicyYaml, "-n", clusterNamespaceOnHub, "--ignore-not-found=true")
		Expect(err).To(BeNil())
		opt := metav1.ListOptions{}
		utils.ListWithTimeout(clientManagedDynamic, gvrPolicy, opt, 0, true, defaultTimeoutSeconds)
		By("clean up all events")
		_, err = kubectlManaged("delete", "events", "-n", clusterNamespace, "--all")
		Expect(err).Should(BeNil())
	})
	It("Should restore owner references that are edited out of the child config policy", func() {
		By("Patching config policy to remove owner references")
		Eventually(func() error {
			_, err := kubectlManaged("patch", "configurationpolicy", case16ConfigPolicyName, "-n", clusterNamespace,
				"--type", "merge", "--patch-file", case16PatchConfigPolicyYaml)

			return err
		}, defaultTimeoutSeconds, 1).Should(BeNil())

		Eventually(func() interface{} {
			configPlc := utils.GetWithTimeout(clientManagedDynamic, gvrConfigurationPolicy,
				case16ConfigPolicyName, clusterNamespace, true, defaultTimeoutSeconds)

			md, ok := configPlc.Object["metadata"].(map[string]interface{})
			if !ok {
				return nil
			}
			ownerRefs, ok := md["ownerReferences"]
			if !ok {
				return nil
			}

			return ownerRefs.([]interface{})[0].(map[string]interface{})["name"]
		}, defaultTimeoutSeconds, 1).Should(utils.SemanticEqual(case16PolicyName))
	})
})
