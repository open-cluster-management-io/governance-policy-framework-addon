// Copyright (c) 2023 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package e2e

import (
	"errors"
	"os/exec"

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
)

func checkInformAction(cfplc string) {
	By("Checking template policy remediationAction")
	Eventually(func() interface{} {
		plc := utils.GetWithTimeout(clientManagedDynamic, gvrConfigurationPolicy,
			cfplc, clusterNamespace, true, defaultTimeoutSeconds)

		return plc.Object["spec"].(map[string]interface{})["remediationAction"]
	}, defaultTimeoutSeconds, 1).Should(Equal("inform"))
}

var _ = Describe("Test 'InformOnly' ConfigurationPolicies", Ordered, func() {
	AfterAll(func() {
		By("Deleting all policies on the hub in ns:" + clusterNamespaceOnHub)
		_, err := kubectlHub("delete", "-f", case20PolicyYaml, "-n", clusterNamespaceOnHub,
			"--ignore-not-found")
		var e *exec.ExitError
		if !errors.As(err, &e) {
			Expect(err).ShouldNot(HaveOccurred())
		}

		_, err = kubectlHub("delete", "-f", case20PolicyNoRemediationYaml, "-n", clusterNamespaceOnHub,
			"--ignore-not-found")
		if !errors.As(err, &e) {
			Expect(err).ShouldNot(HaveOccurred())
		}
	})

	Describe("Override remediationAction in spec", func() {
		Context("When parent policy have remediationAction=enforce", func() {
			It("Should have remediationAction=inform", func() {
				By("Applying parent policy " + case20PolicyName + " in hub ns: " + clusterNamespaceOnHub)
				hubApplyPolicy(case20PolicyName, case20PolicyYaml)
				checkInformAction(case20ConfigPlcName)
			})
		})

		Context("When parent policy have no remediationAction field set", func() {
			It("Should have remediationAction=inform", func() {
				By("Applying parent policy " + case20PolicyNoRemediationName + " in hub ns: " + clusterNamespaceOnHub)
				hubApplyPolicy(case20PolicyNoRemediationName, case20PolicyNoRemediationYaml)
				checkInformAction(case20ConfigPlcNoRemediationName)
			})
		})
	})
})
