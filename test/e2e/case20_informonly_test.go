// Copyright (c) 2023 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package e2e

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"open-cluster-management.io/config-policy-controller/test/utils"
)

const (
	case20PolicyName                 string = "case20-policy-informonly"
	case20PolicyYaml                 string = "../resources/case20_policy_informonly/case20-parent-policy.yaml"
	case20ConfigPlcName              string = "create-configmap"
	case20ConfigPlcNoRemediationName string = "create-configmap-no-remediationaction"
	case20PolicyNoRemediationName    string = "case20-policy-informonly-no-remediationaction"
	case20PolicyNoRemediationYaml    string = "../resources/case20_policy_informonly/" +
		"case20-parent-policy-noremediation.yaml"
	case20PlcTemplateNoRemediationName string = "case20-policy-template-no-remediationaction"
	case20PlcTemplateNoRemediationYaml string = "../resources/case20_policy_informonly/" +
		"case20-policy-template-noremediation.yaml"
	case20ConfigPlcTemplateNoRemediationName string = "create-configmap-policy-template"
)

func checkInformAction(cfplc string, compliance string) {
	By("Checking template policy remediationAction")
	Eventually(func() interface{} {
		plc := utils.GetWithTimeout(clientManagedDynamic, gvrConfigurationPolicy,
			cfplc, clusterNamespace, true, defaultTimeoutSeconds)

		return plc.Object["spec"].(map[string]interface{})["remediationAction"]
	}, defaultTimeoutSeconds, 1).Should(Equal(compliance))
}

var _ = Describe("Test 'InformOnly' ConfigurationPolicies", Ordered, func() {
	AfterAll(func() {
		By("Deleting all policies on the hub in ns:" + clusterNamespaceOnHub)
		_, err := kubectlHub("delete", "-f", case20PolicyYaml, "-n", clusterNamespaceOnHub,
			"--ignore-not-found")
		Expect(err).ShouldNot(HaveOccurred())

		_, err = kubectlHub("delete", "-f", case20PolicyNoRemediationYaml, "-n", clusterNamespaceOnHub,
			"--ignore-not-found")
		Expect(err).ShouldNot(HaveOccurred())

		_, err = kubectlHub("delete", "-f", case20PlcTemplateNoRemediationYaml, "-n", clusterNamespaceOnHub,
			"--ignore-not-found")
		Expect(err).ShouldNot(HaveOccurred())
	})

	Describe("Override remediationAction in spec", func() {
		Context("When parent policy have remediationAction=enforce", func() {
			It("Should have remediationAction=inform", func() {
				By("Applying parent policy " + case20PolicyName + " in hub ns: " + clusterNamespaceOnHub)
				hubApplyPolicy(case20PolicyName, case20PolicyYaml)
				checkInformAction(case20ConfigPlcName, "inform")
			})
		})

		Context("When parent policy have no remediationAction field set", func() {
			It("Should have remediationAction=inform", func() {
				By("Applying parent policy " + case20PolicyNoRemediationName + " in hub ns: " + clusterNamespaceOnHub)
				hubApplyPolicy(case20PolicyNoRemediationName, case20PolicyNoRemediationYaml)
				checkInformAction(case20ConfigPlcNoRemediationName, "inform")
			})
		})

		Context("When policy template have no remediationAction field set", func() {
			It("Should have inherited parent policy's remediationAction field", func() {
				By("Applying parent policy " + case20PlcTemplateNoRemediationName + " in hub ns: " +
					clusterNamespaceOnHub)
				hubApplyPolicy(case20PlcTemplateNoRemediationName, case20PlcTemplateNoRemediationYaml)
				checkInformAction(case20ConfigPlcTemplateNoRemediationName, "inform")
			})
		})
	})
})
