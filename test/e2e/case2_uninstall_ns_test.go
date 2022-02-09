// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package e2e

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"open-cluster-management.io/governance-policy-propagator/test/utils"
)

const (
	case2PolicyYaml    string = "../resources/case2_uninstall_ns/case2-test-policy.yaml"
	case2UninstallYaml string = "../resources/case2_uninstall_ns/case2-uninstall-ns.yaml"
)

var _ = Describe("Test uninstall ns", func() {
	BeforeEach(func() {
		By("Creating a ns on managed cluster")
		_, err := utils.KubectlWithOutput("create", "ns", "uninstall",
			"--kubeconfig=../../kubeconfig_managed")
		Expect(err).Should(BeNil())
		Eventually(func() interface{} {
			_, err := clientManaged.CoreV1().Namespaces().Get(context.TODO(), "uninstall", metav1.GetOptions{})

			return err
		}, defaultTimeoutSeconds, 1).Should(BeNil())
		By("Creating a policy on mananged cluster in ns: uninstall")
		_, err = utils.KubectlWithOutput("apply", "-f", case2PolicyYaml, "-n", "uninstall",
			"--kubeconfig=../../kubeconfig_managed")
		Expect(err).Should(BeNil())
		opt := metav1.ListOptions{}
		utils.ListWithTimeout(clientManagedDynamic, gvrPolicy, opt, 1, true, defaultTimeoutSeconds)
	})
	AfterEach(func() {
		By("Delete the job on managed cluster")
		_, err := utils.KubectlWithOutput("delete", "job", "uninstall-ns", "-n", "open-cluster-management-agent-addon",
			"--kubeconfig=../../kubeconfig_managed")
		Expect(err).Should(BeNil())
	})
	It("should remove ns on managed cluster", func() {
		By("Running uninstall ns job")
		_, err := utils.KubectlWithOutput(
			"apply",
			"-f",
			case2UninstallYaml,
			"-n",
			"open-cluster-management-agent-addon",
			"--kubeconfig=../../kubeconfig_managed")
		Expect(err).Should(BeNil())
		By("Checking if ns uninstall has been deleted eventually")
		Eventually(func() interface{} {
			_, err := clientManaged.CoreV1().Namespaces().Get(context.TODO(), "uninstall", metav1.GetOptions{})

			return errors.IsNotFound(err)
		}, 120, 1).Should(BeTrue())
	})
})
