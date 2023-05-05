// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package e2e

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	propagatorutils "open-cluster-management.io/governance-policy-propagator/test/utils"
)

var _ = Describe("Test disabled Gatekeeper sync", Ordered, Label("skip-minimum"), func() {
	const (
		caseNumber   string = "case19"
		yamlBasePath string = "../resources/" + caseNumber + "_gatekeeper_disable/"
		policyName   string = caseNumber + "-gk-policy"
		policyYaml   string = yamlBasePath + policyName + ".yaml"
	)

	BeforeAll(func() {
		if !gkSyncDisabled {
			Skip("Gatekeeper sync is enabled--skipping disabled syncing check")
		}
	})

	AfterAll(func() {
		for _, pName := range []string{policyName} {
			By("Deleting policy " + pName + " on the hub in ns:" + clusterNamespaceOnHub)
			err := clientHubDynamic.Resource(gvrPolicy).Namespace(clusterNamespaceOnHub).Delete(
				context.TODO(), pName, metav1.DeleteOptions{},
			)
			if !k8serrors.IsNotFound(err) {
				Expect(err).ToNot(HaveOccurred())
			}

			By("Cleaning up the events for the policy " + pName)
			_, err = kubectlManaged("delete", "events",
				"-n", clusterNamespace,
				"--field-selector=involvedObject.name="+pName,
				"--ignore-not-found",
			)
			Expect(err).ToNot(HaveOccurred())
		}

		opt := metav1.ListOptions{}
		propagatorutils.ListWithTimeout(clientManagedDynamic, gvrPolicy, opt, 0, true, defaultTimeoutSeconds)
	})

	It("should create the policy on the managed cluster", func() {
		By("Creating policy " + policyName + " on the hub in ns:" + clusterNamespaceOnHub)
		_, err := kubectlHub("apply", "-f", policyYaml, "-n", clusterNamespaceOnHub)
		Expect(err).ShouldNot(HaveOccurred())

		By("Verifying policy " + policyName + " synced to the managed cluster and is NonCompliant")
		Eventually(func(g Gomega) {
			plc := propagatorutils.GetWithTimeout(clientManagedDynamic, gvrPolicy, policyName, clusterNamespace, true,
				defaultTimeoutSeconds)
			g.Expect(plc).NotTo(BeNil())
			compliance, _, err := unstructured.NestedString(plc.Object, "status", "compliant")
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(compliance).To(Equal("NonCompliant"))
		}, defaultTimeoutSeconds, 1).Should(Succeed())

		By("Verifying policy " + policyName + " has a disabled Gatekeeper message")
		Eventually(func(g Gomega) {
			plc := propagatorutils.GetWithTimeout(clientHubDynamic, gvrPolicy, policyName, clusterNamespaceOnHub, true,
				defaultTimeoutSeconds)
			details, _, err := unstructured.NestedSlice(plc.Object, "status", "details")
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(details).ToNot(BeEmpty())
			history, _, err := unstructured.NestedSlice(details[0].(map[string]interface{}), "history")
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(history).ToNot(BeEmpty())
			message, _, err := unstructured.NestedString(history[0].(map[string]interface{}), "message")
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(message).To(ContainSubstring("the Gatekeeper integration is disabled"))
		}, defaultTimeoutSeconds, 1).Should(Succeed())
	})
})
