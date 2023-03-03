// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package e2e

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	propagatorutils "open-cluster-management.io/governance-policy-propagator/test/utils"

	"open-cluster-management.io/governance-policy-framework-addon/test/utils"
)

var _ = Describe("Test Gatekeeper ConstraintTemplate sync", Ordered, Label("skip-minimum"), func() {
	const (
		caseNumber                string = "case17"
		yamlBasePath              string = "../resources/" + caseNumber + "_gatekeeper_sync/"
		policyName                string = caseNumber + "-gk-policy"
		policyYaml                string = yamlBasePath + policyName + ".yaml"
		policyYamlExtra           string = yamlBasePath + policyName + "-extra.yaml"
		policyName2               string = policyName + "-2"
		policyYaml2               string = yamlBasePath + policyName2 + ".yaml"
		gkConstraintTemplateName  string = caseNumber + "constrainttemplate"
		gkConstraintTemplateYaml  string = yamlBasePath + gkConstraintTemplateName + ".yaml"
		gkConstraintTmplNameExtra string = gkConstraintTemplateName + "extra"
		gkConstraintTmplYamlExtra string = yamlBasePath + gkConstraintTmplNameExtra + ".yaml"
		gkConstraintName          string = caseNumber + "-gk-constraint"
		gkConstraintYaml          string = yamlBasePath + gkConstraintName + ".yaml"
		gkConstraintName2         string = gkConstraintName + "-2"
		gkConstraintYaml2         string = yamlBasePath + gkConstraintName2 + ".yaml"
		gkConstraintNameExtra     string = gkConstraintName + "-extra"
		gkConstraintYamlExtra     string = yamlBasePath + gkConstraintNameExtra + ".yaml"
	)
	gvrConstraint := schema.GroupVersionResource{
		Group:    gvConstraintGroup,
		Version:  "v1beta1",
		Resource: caseNumber + "constrainttemplate",
	}
	BeforeAll(func() {
		DeployGatekeeper()
	})
	AfterAll(func() {
		for _, pName := range []string{policyName, policyName2} {
			By("Deleting policy " + pName + " on the hub in ns:" + clusterNamespaceOnHub)
			err := clientHubDynamic.Resource(gvrPolicy).Namespace(clusterNamespaceOnHub).Delete(
				context.TODO(), pName, metav1.DeleteOptions{},
			)
			if !errors.IsNotFound(err) {
				Expect(err).To(BeNil())
			}
			By("Cleaning up the events from policy " + pName)
			_, err = kubectlManaged("delete", "events", "-n", clusterNamespace, "--ignore-not-found",
				"--field-selector=involvedObject.name="+pName,
			)
			Expect(err).To(BeNil())
		}
		opt := metav1.ListOptions{}
		propagatorutils.ListWithTimeout(clientManagedDynamic, gvrPolicy, opt, 0, true, defaultTimeoutSeconds)

		By("Deleting Gatekeeper " + utils.GkVersion + " from the managed cluster")
		_, err := kubectlManaged("delete", "-f", utils.GkDeployment)
		Expect(err).Should(BeNil())
	})
	It("should create the policy on the managed cluster", func() {
		By("Creating policy " + policyName + " on the hub in ns:" + clusterNamespaceOnHub)
		_, err := kubectlHub("apply", "-f", policyYaml, "-n", clusterNamespaceOnHub)
		Expect(err).Should(BeNil())
		plc := propagatorutils.GetWithTimeout(clientManagedDynamic, gvrPolicy, policyName, clusterNamespace, true,
			defaultTimeoutSeconds)
		Expect(plc).NotTo(BeNil())
		By("Creating policy " + policyName2 + " on the hub in ns:" + clusterNamespaceOnHub)
		_, err = kubectlHub("apply", "-f", policyYaml2, "-n", clusterNamespaceOnHub)
		Expect(err).Should(BeNil())
		plc = propagatorutils.GetWithTimeout(clientManagedDynamic, gvrPolicy, policyName, clusterNamespace, true,
			defaultTimeoutSeconds)
		Expect(plc).NotTo(BeNil())
	})
	It("should create Gatekeeper constraints on the managed cluster", func() {
		By("Checking for the synced ConstraintTemplate " + gkConstraintTemplateName)
		expectedConstraintTemplate := propagatorutils.ParseYaml(gkConstraintTemplateYaml)
		Eventually(func() interface{} {
			trustedPlc := propagatorutils.GetWithTimeout(clientManagedDynamic, gvrConstraintTemplate,
				gkConstraintTemplateName, "", true, defaultTimeoutSeconds)

			return trustedPlc.Object["spec"]
		}, defaultTimeoutSeconds, 1).Should(propagatorutils.SemanticEqual(expectedConstraintTemplate.Object["spec"]))
		By("Checking for the synced Constraint " + gkConstraintName)
		expectedConstraint := propagatorutils.ParseYaml(gkConstraintYaml)
		Eventually(func() interface{} {
			trustedPlc := propagatorutils.GetWithTimeout(clientManagedDynamic, gvrConstraint,
				gkConstraintName, "", true, defaultTimeoutSeconds)

			return trustedPlc.Object["spec"]
		}, defaultTimeoutSeconds, 1).Should(propagatorutils.SemanticEqual(expectedConstraint.Object["spec"]))
		By("Checking for the synced Constraint " + gkConstraintName2)
		expectedConstraint2 := propagatorutils.ParseYaml(gkConstraintYaml2)
		Eventually(func() interface{} {
			trustedPlc := propagatorutils.GetWithTimeout(clientManagedDynamic, gvrConstraint,
				gkConstraintName2, "", true, defaultTimeoutSeconds)

			return trustedPlc.Object["spec"]
		}, defaultTimeoutSeconds, 1).Should(propagatorutils.SemanticEqual(expectedConstraint2.Object["spec"]))
	})
	It("should set status for the ConstraintTemplate to Compliant", func() {
		By("Checking if policy status is compliant for the ConstraintTemplate")
		managedPlc := propagatorutils.GetWithTimeout(clientManagedDynamic, gvrPolicy, policyName, clusterNamespace,
			true, defaultTimeoutSeconds)
		Expect(managedPlc).NotTo(BeNil())

		Eventually(func() string {
			var compliance string
			detailsSlice, found, err := unstructured.NestedSlice(managedPlc.Object, "status", "details")
			if found {
				compliance, _, _ = unstructured.NestedString(detailsSlice[0].(map[string]interface{}), "compliant")
			} else if err != nil {
				GinkgoWriter.Printf("Failed to retrieve compliance: %s\n", err)
			}

			return compliance
		}, defaultTimeoutSeconds, 1).Should(Equal("Compliant"))
	})
	It("should add a Constraint and ConstraintTemplate when added to policy-templates", func() {
		By("Adding a Constraint and ConstraintTemplate to the policy-templates array")
		_, err := kubectlHub("apply", "-f", policyYamlExtra, "-n", clusterNamespaceOnHub)
		Expect(err).To(BeNil())
		By("Checking for the synced Constraint " + gkConstraintNameExtra)
		expectedConstraint := propagatorutils.ParseYaml(gkConstraintYamlExtra)
		Eventually(func() interface{} {
			trustedPlc := propagatorutils.GetWithTimeout(clientManagedDynamic, gvrConstraint,
				gkConstraintNameExtra, "", true, defaultTimeoutSeconds)

			return trustedPlc.Object["spec"]
		}, defaultTimeoutSeconds, 1).Should(propagatorutils.SemanticEqual(expectedConstraint.Object["spec"]))
		By("Checking for the synced ConstraintTemplate " + gkConstraintTmplNameExtra)
		expectedConstraintTmpl := propagatorutils.ParseYaml(gkConstraintTmplYamlExtra)
		Eventually(func() interface{} {
			trustedPlc := propagatorutils.GetWithTimeout(clientManagedDynamic, gvrConstraintTemplate,
				gkConstraintTmplNameExtra, "", true, defaultTimeoutSeconds)

			return trustedPlc.Object["spec"]
		}, defaultTimeoutSeconds, 1).Should(propagatorutils.SemanticEqual(expectedConstraintTmpl.Object["spec"]))
	})
	It("should remove a Constraint and ConstraintTemplate when removed from policy-templates", func() {
		By("Removing a Constraint and ConstraintTemplate from the policy-templates array")
		_, err := kubectlHub("apply", "-f", policyYaml, "-n", clusterNamespaceOnHub)
		Expect(err).To(BeNil())
		By("Checking for removed Constraint " + gkConstraintNameExtra)
		Eventually(func() interface{} {
			return propagatorutils.GetWithTimeout(clientManagedDynamic, gvrConstraint,
				gkConstraintNameExtra, "", false, defaultTimeoutSeconds)
		}, defaultTimeoutSeconds, 1).Should(BeNil())
		By("Checking for removed ConstraintTemplate " + gkConstraintTmplNameExtra)
		Eventually(func() interface{} {
			return propagatorutils.GetWithTimeout(clientManagedDynamic, gvrConstraintTemplate,
				gkConstraintTmplNameExtra, "", false, defaultTimeoutSeconds)
		}, defaultTimeoutSeconds, 1).Should(BeNil())
	})
	It("should delete template policy on managed cluster", func() {
		By("Deleting parent policies")
		_, err := kubectlHub("delete", "-f", policyYaml, "-n", clusterNamespaceOnHub)
		Expect(err).Should(BeNil())
		_, err = kubectlHub("delete", "-f", policyYaml2, "-n", clusterNamespaceOnHub)
		Expect(err).Should(BeNil())
		opt := metav1.ListOptions{}
		propagatorutils.ListWithTimeout(clientManagedDynamic, gvrPolicy, opt, 0, true, defaultTimeoutSeconds)
		By("Checking for the existence of ConstraintTemplate " + gkConstraintTemplateName)
		propagatorutils.GetWithTimeout(clientManagedDynamic, gvrConstraintTemplate, gkConstraintTemplateName,
			"", false, defaultTimeoutSeconds,
		)
		By("Checking for the existence of Constraint " + gkConstraintName)
		propagatorutils.GetWithTimeout(clientManagedDynamic, gvrConstraint, gkConstraintName,
			"", false, defaultTimeoutSeconds,
		)
		By("Checking for the existence of Constraint " + gkConstraintName2)
		propagatorutils.GetWithTimeout(clientManagedDynamic, gvrConstraint, gkConstraintName2,
			"", false, defaultTimeoutSeconds,
		)
	})
})
