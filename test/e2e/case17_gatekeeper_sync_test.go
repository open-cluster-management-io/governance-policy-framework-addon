// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package e2e

import (
	"context"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	policyv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	propagatorutils "open-cluster-management.io/governance-policy-propagator/test/utils"

	"open-cluster-management.io/governance-policy-framework-addon/controllers/gatekeepersync"
)

var _ = Describe("Test Gatekeeper ConstraintTemplate and constraint sync", Ordered, Label("skip-minimum"), func() {
	const (
		caseNumber                string        = "case17"
		configMapName             string        = caseNumber + "-test"
		configMap2Name            string        = caseNumber + "-test2"
		configMap3Name            string        = caseNumber + "-test3"
		configMapNamespace        string        = caseNumber + "-gk-test"
		yamlBasePath              string        = "../resources/" + caseNumber + "_gatekeeper_sync/"
		policyName                string        = caseNumber + "-gk-policy"
		policyYaml                string        = yamlBasePath + policyName + ".yaml"
		policyYamlExtra           string        = yamlBasePath + policyName + "-extra.yaml"
		policyName2               string        = policyName + "-2"
		policyYaml2               string        = yamlBasePath + policyName2 + ".yaml"
		gkAuditFrequency          time.Duration = time.Minute
		gkConstraintTemplateName  string        = caseNumber + "constrainttemplate"
		gkConstraintTemplateYaml  string        = yamlBasePath + gkConstraintTemplateName + ".yaml"
		gkConstraintTmplNameExtra string        = gkConstraintTemplateName + "extra"
		gkConstraintTmplYamlExtra string        = yamlBasePath + gkConstraintTmplNameExtra + ".yaml"
		gkConstraintName          string        = caseNumber + "-gk-constraint"
		gkConstraintYaml          string        = yamlBasePath + gkConstraintName + ".yaml"
		gkConstraintName2         string        = gkConstraintName + "-2"
		gkConstraintYaml2         string        = yamlBasePath + gkConstraintName2 + ".yaml"
		gkConstraintNameExtra     string        = gkConstraintName + "-extra"
		gkConstraintYamlExtra     string        = yamlBasePath + gkConstraintNameExtra + ".yaml"
	)
	gvrConstraint := schema.GroupVersionResource{
		Group:    gvConstraintGroup,
		Version:  "v1beta1",
		Resource: caseNumber + "constrainttemplate",
	}

	BeforeAll(func() {
		if gkSyncDisabled {
			Skip("Gatekeeper sync is disabled--skipping Gatekeeper tests")
		}

		By("Creating the namespace " + configMapNamespace)
		ns := &corev1.Namespace{
			TypeMeta: metav1.TypeMeta{
				Kind:       "Namespace",
				APIVersion: "v1",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: configMapNamespace,
			},
		}

		_, err := clientManaged.CoreV1().Namespaces().Create(context.TODO(), ns, metav1.CreateOptions{})
		Expect(err).ToNot(HaveOccurred())

		Eventually(func(g Gomega) {
			deployments, err := clientManaged.AppsV1().Deployments("gatekeeper-system").
				List(context.TODO(), metav1.ListOptions{})
			g.Expect(err).ShouldNot(HaveOccurred())
			g.Expect(deployments.Items).ToNot(BeEmpty())

			var available bool
			for _, deployment := range deployments.Items {
				for _, condition := range deployment.Status.Conditions {
					if condition.Reason == "MinimumReplicasAvailable" {
						available = condition.Status == "True"
					}
				}
				g.Expect(available).To(BeTrue())
			}
		}, defaultTimeoutSeconds, 1).Should(Succeed())
	})

	AfterAll(func() {
		By("Deleting the namespace " + configMapNamespace)
		err := clientManaged.CoreV1().Namespaces().Delete(context.TODO(), configMapNamespace, metav1.DeleteOptions{})
		if !k8serrors.IsNotFound(err) {
			Expect(err).ToNot(HaveOccurred())
		}

		for _, pName := range []string{policyName, policyName2} {
			By("Deleting policy " + pName + " on the hub in ns:" + clusterNamespaceOnHub)
			err := clientHubDynamic.Resource(gvrPolicy).Namespace(clusterNamespaceOnHub).Delete(
				context.TODO(), pName, metav1.DeleteOptions{},
			)
			if !k8serrors.IsNotFound(err) {
				Expect(err).ToNot(HaveOccurred())
			}

			By("Cleaning up the events for the policy " + pName)
			_, err = kubectlManaged(
				"delete",
				"events",
				"-n",
				clusterNamespace,
				"--field-selector=involvedObject.name="+pName,
				"--ignore-not-found",
			)
			Expect(err).ToNot(HaveOccurred())
		}

		opt := metav1.ListOptions{}
		propagatorutils.ListWithTimeout(clientManagedDynamic, gvrPolicy, opt, 0, true, defaultTimeoutSeconds)

		By("Fixing the Gatekeeper webhook if required")
		Eventually(
			func(g Gomega) {
				webhook, err := clientManaged.AdmissionregistrationV1().ValidatingWebhookConfigurations().Get(
					context.TODO(), gatekeepersync.GatekeeperWebhookName, metav1.GetOptions{},
				)
				g.Expect(err).ToNot(HaveOccurred())

				fixed := false

				for i := range webhook.Webhooks {
					if strings.HasPrefix(webhook.Webhooks[i].Name, "not-") {
						webhook.Webhooks[i].Name = strings.TrimPrefix(webhook.Webhooks[i].Name, "not-")
						fixed = true
					}
				}

				if !fixed {
					return
				}

				By("Updating the Gatekeeper webhook")
				_, err = clientManaged.AdmissionregistrationV1().ValidatingWebhookConfigurations().Update(
					context.TODO(), webhook, metav1.UpdateOptions{},
				)
				g.Expect(err).ToNot(HaveOccurred())
			},
			defaultTimeoutSeconds,
			1,
		).Should(Succeed())

		By("Waiting for the namespace " + configMapNamespace + " to be deleted")
		Eventually(
			func(g Gomega) {
				_, err := clientManaged.CoreV1().Namespaces().Get(
					context.TODO(), configMapNamespace, metav1.GetOptions{},
				)
				g.Expect(k8serrors.IsNotFound(err)).To(BeTrue())
			},
			defaultTimeoutSeconds*2,
			1,
		).Should(Succeed())
	})

	It("should create the policy on the managed cluster", func() {
		By("Creating policy " + policyName + " on the hub in ns:" + clusterNamespaceOnHub)
		_, err := kubectlHub("apply", "-f", policyYaml, "-n", clusterNamespaceOnHub)
		Expect(err).ShouldNot(HaveOccurred())
		plc := propagatorutils.GetWithTimeout(clientManagedDynamic, gvrPolicy, policyName, clusterNamespace, true,
			defaultTimeoutSeconds)
		Expect(plc).NotTo(BeNil())
		By("Creating policy " + policyName2 + " on the hub in ns:" + clusterNamespaceOnHub)
		_, err = kubectlHub("apply", "-f", policyYaml2, "-n", clusterNamespaceOnHub)
		Expect(err).ShouldNot(HaveOccurred())
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
		Eventually(func() string {
			managedPlc := propagatorutils.GetWithTimeout(clientManagedDynamic, gvrPolicy, policyName, clusterNamespace,
				true, defaultTimeoutSeconds)
			Expect(managedPlc).NotTo(BeNil())

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

	It("should return Gatekeeper audit results", func() {
		By("Checking if policy status is compliant for the constraint")
		Eventually(func(g Gomega) {
			managedPolicyUnstructured := propagatorutils.GetWithTimeout(
				clientManagedDynamic, gvrPolicy, policyName, clusterNamespace, true, defaultTimeoutSeconds,
			)

			managedPolicy := policyv1.Policy{}
			err := runtime.DefaultUnstructuredConverter.FromUnstructured(
				managedPolicyUnstructured.Object, &managedPolicy,
			)
			g.Expect(err).ToNot(HaveOccurred())

			g.Expect(managedPolicy.Status.Details).To(HaveLen(2))
			g.Expect(managedPolicy.Status.Details[1].TemplateMeta.GetName()).To(Equal(gkConstraintName))
			history := managedPolicy.Status.Details[1].History
			g.Expect((history)).ToNot(BeEmpty())
			expectedMsg := "Compliant; The constraint has no violations"
			g.Expect(history[0].Message).To(
				Equal(expectedMsg),
				fmt.Sprintf("Got %s but expected %s", history[0].Message, expectedMsg),
			)
		}, gkAuditFrequency*3, 1).Should(Succeed())

		By("Adding ConfigMaps that violate the constraint")
		for _, cmName := range []string{configMapName, configMap2Name} {
			configMap := &corev1.ConfigMap{
				TypeMeta: metav1.TypeMeta{
					Kind:       "ConfigMap",
					APIVersion: "v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      cmName,
					Namespace: configMapNamespace,
				},
			}

			_, err := clientManaged.CoreV1().ConfigMaps(configMapNamespace).Create(
				context.TODO(), configMap, metav1.CreateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
		}

		By("Checking if policy status is noncompliant for the constraint")
		Eventually(func(g Gomega) {
			managedPolicyUnstructured := propagatorutils.GetWithTimeout(
				clientManagedDynamic, gvrPolicy, policyName, clusterNamespace, true, defaultTimeoutSeconds,
			)

			managedPolicy := policyv1.Policy{}
			err := runtime.DefaultUnstructuredConverter.FromUnstructured(
				managedPolicyUnstructured.Object, &managedPolicy,
			)
			g.Expect(err).ToNot(HaveOccurred())

			history := managedPolicy.Status.Details[1].History
			g.Expect((history)).ToNot(BeEmpty())
			validMsgs := []string{
				`NonCompliant; warn - All configmaps must have a 'my-gk-test' label (on ConfigMap ` +
					`case17-gk-test/case17-test); warn - All configmaps must have a 'my-gk-test' label ` +
					`(on ConfigMap case17-gk-test/case17-test2)`,
				`NonCompliant; warn - All configmaps must have a 'my-gk-test' label (on ConfigMap ` +
					`case17-gk-test/case17-test2); warn - All configmaps must have a 'my-gk-test' label ` +
					`(on ConfigMap case17-gk-test/case17-test)`,
			}
			g.Expect(validMsgs).To(
				ContainElement(history[0].Message),
				fmt.Sprintf("Got %s but expected one of %v", history[0].Message, validMsgs),
			)

			// Verify that there are no duplicate gatekeeper status messages.
			for i, historyEvent := range managedPolicy.Status.Details[1].History {
				if i == 0 || strings.Contains(historyEvent.Message, "NonCompliant; template-error;") {
					continue
				}

				g.Expect(managedPolicy.Status.Details[1].History[i-1].Message).ToNot(Equal(historyEvent.Message))
			}
		}, gkAuditFrequency*3, 1).Should(Succeed())
	})

	It("should deny an invalid ConfigMap when remediationAction=enforce", func() {
		By("Patching the remediationAction to enforce")
		Eventually(
			func() interface{} {
				managedPlc := propagatorutils.GetWithTimeout(
					clientHubDynamic, gvrPolicy, policyName, clusterNamespaceOnHub, true, defaultTimeoutSeconds,
				)
				_, err := patchRemediationAction(clientHubDynamic, managedPlc, "enforce")

				return err
			},
			defaultTimeoutSeconds,
			1,
		).Should(BeNil())

		By("Waiting for the Constraint to have enforcementAction=deny")
		Eventually(
			func(g Gomega) {
				constraint, err := clientManagedDynamic.Resource(gvrConstraint).Get(
					context.TODO(), gkConstraintName, metav1.GetOptions{},
				)
				g.Expect(err).ToNot(HaveOccurred())

				action, _, _ := unstructured.NestedString(constraint.Object, "spec", "enforcementAction")
				g.Expect(action).To(Equal("deny"))
			},
			defaultTimeoutSeconds,
			1,
		).Should(Succeed())

		By("Trying to create a ConfigMap that violates the constraint")
		configMap := &corev1.ConfigMap{
			TypeMeta: metav1.TypeMeta{
				Kind:       "ConfigMap",
				APIVersion: "v1",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      configMap3Name,
				Namespace: configMapNamespace,
			},
		}

		_, err := clientManaged.CoreV1().ConfigMaps(configMapNamespace).Create(
			context.TODO(), configMap, metav1.CreateOptions{},
		)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(
			Equal(
				`admission webhook "validation.gatekeeper.sh" denied the request: [` + gkConstraintName + `] All ` +
					`configmaps must have a 'my-gk-test' label`,
			),
		)
	})

	It("should have an error if the remediationAction=enforce and the Gatekeeper webhook is not enabled", func() {
		By("Renaming the Gatekeeper webhook")
		Eventually(
			func(g Gomega) {
				webhook, err := clientManaged.AdmissionregistrationV1().ValidatingWebhookConfigurations().Get(
					context.TODO(), gatekeepersync.GatekeeperWebhookName, metav1.GetOptions{},
				)
				g.Expect(err).ToNot(HaveOccurred())

				for i := range webhook.Webhooks {
					webhook.Webhooks[i].Name = "not-" + webhook.Webhooks[i].Name
				}

				_, err = clientManaged.AdmissionregistrationV1().ValidatingWebhookConfigurations().Update(
					context.TODO(), webhook, metav1.UpdateOptions{},
				)
				g.Expect(err).ToNot(HaveOccurred())
			},
			defaultTimeoutSeconds,
			1,
		).Should(Succeed())

		By("Checking if policy status is noncompliant for the constraint")
		Eventually(func(g Gomega) {
			managedPolicyUnstructured := propagatorutils.GetWithTimeout(
				clientManagedDynamic, gvrPolicy, policyName, clusterNamespace, true, defaultTimeoutSeconds,
			)

			managedPolicy := policyv1.Policy{}
			err := runtime.DefaultUnstructuredConverter.FromUnstructured(
				managedPolicyUnstructured.Object, &managedPolicy,
			)
			g.Expect(err).ToNot(HaveOccurred())

			history := managedPolicy.Status.Details[1].History
			g.Expect((history)).ToNot(BeEmpty())
			validMsgs := []string{
				`NonCompliant; The Gatekeeper validating webhook is disabled but the constraint's ` +
					`spec.enforcementAction is deny. deny - All configmaps must have a 'my-gk-test' label ` +
					`(on ConfigMap case17-gk-test/case17-test); deny - All configmaps must have a 'my-gk-test' ` +
					`label (on ConfigMap case17-gk-test/case17-test2)`,
				`NonCompliant; The Gatekeeper validating webhook is disabled but the constraint's ` +
					`spec.enforcementAction is deny. deny - All configmaps must have a 'my-gk-test' label ` +
					`(on ConfigMap case17-gk-test/case17-test2); deny - All configmaps must have a 'my-gk-test' ` +
					`label (on ConfigMap case17-gk-test/case17-test)`,
			}
			g.Expect(validMsgs).To(
				ContainElement(history[0].Message),
				fmt.Sprintf("Got %s but expected one of %v", history[0].Message, validMsgs),
			)
		}, gkAuditFrequency*3, 1).Should(Succeed())

		By("Restoring the Gatekeeper webhook")
		Eventually(
			func(g Gomega) {
				webhook, err := clientManaged.AdmissionregistrationV1().ValidatingWebhookConfigurations().Get(
					context.TODO(), gatekeepersync.GatekeeperWebhookName, metav1.GetOptions{},
				)
				g.Expect(err).ToNot(HaveOccurred())

				for i := range webhook.Webhooks {
					webhook.Webhooks[i].Name = strings.TrimPrefix(webhook.Webhooks[i].Name, "not-")
				}

				_, err = clientManaged.AdmissionregistrationV1().ValidatingWebhookConfigurations().Update(
					context.TODO(), webhook, metav1.UpdateOptions{},
				)
				g.Expect(err).ToNot(HaveOccurred())
			},
			defaultTimeoutSeconds,
			1,
		).Should(Succeed())
	})

	It("should add a Constraint and ConstraintTemplate when added to policy-templates", func() {
		By("Adding a Constraint and ConstraintTemplate to the policy-templates array")
		_, err := kubectlHub("apply", "-f", policyYamlExtra, "-n", clusterNamespaceOnHub)
		Expect(err).ToNot(HaveOccurred())
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
		Expect(err).ToNot(HaveOccurred())
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
		Expect(err).ShouldNot(HaveOccurred())
		_, err = kubectlHub("delete", "-f", policyYaml2, "-n", clusterNamespaceOnHub)
		Expect(err).ShouldNot(HaveOccurred())
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

	Describe("Test policy ordering with gatekeeper objects", func() {
		const (
			waitForTemplateName   = "case17-gk-dep-on-tmpl"
			waitForTemplateYaml   = yamlBasePath + waitForTemplateName + ".yaml"
			waitForConstraintName = "case17-gk-dep-on-constraint"
			waitForConstraintYaml = yamlBasePath + waitForConstraintName + ".yaml"
		)

		BeforeAll(func(ctx context.Context) {
			By("Deleting any ConfigMaps in the test namespace, to prevent any initial violations")
			err := clientManaged.CoreV1().ConfigMaps(configMapNamespace).DeleteCollection(
				ctx, metav1.DeleteOptions{}, metav1.ListOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
		})

		AfterAll(func() {
			for _, pName := range []string{waitForTemplateName, waitForConstraintName} {
				By("Deleting policy " + pName + " on the hub in ns:" + clusterNamespaceOnHub)
				err := clientHubDynamic.Resource(gvrPolicy).Namespace(clusterNamespaceOnHub).Delete(
					context.TODO(), pName, metav1.DeleteOptions{},
				)
				if !k8serrors.IsNotFound(err) {
					Expect(err).ToNot(HaveOccurred())
				}

				By("Cleaning up the events for the policy " + pName)
				_, err = kubectlManaged(
					"delete",
					"events",
					"-n",
					clusterNamespace,
					"--field-selector=involvedObject.name="+pName,
					"--ignore-not-found",
				)
				Expect(err).ToNot(HaveOccurred())
			}
		})

		It("should not progress until the ConstraintTemplate is created", func() {
			By("Creating policy " + waitForTemplateName + " on the hub in ns:" + clusterNamespaceOnHub)
			_, err := kubectlHub("apply", "-f", waitForTemplateYaml, "-n", clusterNamespaceOnHub)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(propagatorutils.GetWithTimeout(
				clientManagedDynamic,
				gvrPolicy,
				waitForTemplateName,
				clusterNamespace,
				true,
				defaultTimeoutSeconds,
			)).NotTo(BeNil())

			By("Checking that the configuration policy is not found, because it should be pending")
			Consistently(func() interface{} {
				return propagatorutils.GetWithTimeout(
					clientManagedDynamic,
					gvrConfigurationPolicy,
					waitForTemplateName,
					clusterNamespace,
					false,
					defaultTimeoutSeconds)
			}, 10, 1).Should(BeNil())

			By("Creating the policy with the ConstraintTemplate")
			_, err = kubectlHub("apply", "-f", policyYaml, "-n", clusterNamespaceOnHub)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(propagatorutils.GetWithTimeout(
				clientManagedDynamic,
				gvrPolicy,
				policyName,
				clusterNamespace,
				true,
				defaultTimeoutSeconds,
			)).NotTo(BeNil())

			By("Checking that the configuration policy can now be found")
			Expect(propagatorutils.GetWithTimeout(
				clientManagedDynamic,
				gvrConfigurationPolicy,
				waitForTemplateName,
				clusterNamespace,
				true,
				defaultTimeoutSeconds,
			)).NotTo(BeNil())
		})

		It("should progress initially when the constraint has no violations", func() {
			By("Verifying that the policy status of the constraint is compliant")
			Eventually(func(g Gomega) {
				plc := propagatorutils.GetWithTimeout(
					clientManagedDynamic,
					gvrPolicy,
					policyName,
					clusterNamespace,
					true,
					defaultTimeoutSeconds,
				)

				compliance, found, err := unstructured.NestedString(plc.Object, "status", "compliant")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(found).To(BeTrue())
				g.Expect(compliance).To(Equal("Compliant"))
			}, gkAuditFrequency*3, 1).Should(Succeed())

			By("Creating policy " + waitForConstraintName + " on the hub in ns:" + clusterNamespaceOnHub)
			_, err := kubectlHub("apply", "-f", waitForConstraintYaml, "-n", clusterNamespaceOnHub)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(propagatorutils.GetWithTimeout(
				clientManagedDynamic,
				gvrPolicy,
				waitForConstraintName,
				clusterNamespace,
				true,
				defaultTimeoutSeconds,
			)).NotTo(BeNil())

			By("Checking that the configuration policy is found; the policy is not pending")
			Expect(propagatorutils.GetWithTimeout(
				clientManagedDynamic,
				gvrConfigurationPolicy,
				waitForConstraintName,
				clusterNamespace,
				true,
				defaultTimeoutSeconds,
			)).NotTo(BeNil())
		})

		It("should become Pending when there are violations on the constraint", func() {
			By("Adding a ConfigMap that violates the constraint")
			configMap := &corev1.ConfigMap{
				TypeMeta: metav1.TypeMeta{
					Kind:       "ConfigMap",
					APIVersion: "v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      configMapName,
					Namespace: configMapNamespace,
				},
			}

			_, err := clientManaged.CoreV1().ConfigMaps(configMapNamespace).Create(
				context.TODO(), configMap, metav1.CreateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())

			By("Waiting for policy status of the constraint to be noncompliant")
			Eventually(func(g Gomega) {
				plc := propagatorutils.GetWithTimeout(
					clientManagedDynamic,
					gvrPolicy,
					policyName,
					clusterNamespace,
					true,
					defaultTimeoutSeconds,
				)

				compliance, found, err := unstructured.NestedString(plc.Object, "status", "compliant")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(found).To(BeTrue())
				g.Expect(compliance).To(Equal("NonCompliant"))
			}, gkAuditFrequency*3, 1).Should(Succeed())

			By("Checking that the configuration policy is not found, because it should be pending")
			Expect(propagatorutils.GetWithTimeout(
				clientManagedDynamic,
				gvrConfigurationPolicy,
				waitForConstraintName,
				clusterNamespace,
				false,
				defaultTimeoutSeconds,
			)).To(BeNil())
		})

		It("should progress again when the violations are addressed", func() {
			By("Deleting the ConfigMap causing the violation")
			err := clientManaged.CoreV1().ConfigMaps(configMapNamespace).Delete(
				context.TODO(), configMapName, metav1.DeleteOptions{},
			)
			Expect(err).ToNot(HaveOccurred())

			By("Waiting for policy status of the constraint to be compliant")
			Eventually(func(g Gomega) {
				plc := propagatorutils.GetWithTimeout(
					clientManagedDynamic,
					gvrPolicy,
					policyName,
					clusterNamespace,
					true,
					defaultTimeoutSeconds,
				)

				compliance, found, err := unstructured.NestedString(plc.Object, "status", "compliant")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(found).To(BeTrue())
				g.Expect(compliance).To(Equal("Compliant"))
			}, gkAuditFrequency*3, 1).Should(Succeed())

			By("Checking that the configuration policy is found; the policy is no longer pending")
			Expect(propagatorutils.GetWithTimeout(
				clientManagedDynamic,
				gvrConfigurationPolicy,
				waitForConstraintName,
				clusterNamespace,
				true,
				defaultTimeoutSeconds,
			)).NotTo(BeNil())
		})
	})
})
