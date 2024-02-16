// Copyright Contributors to the Open Cluster Management project

package e2e

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	"open-cluster-management.io/governance-policy-propagator/controllers/complianceeventsapi"
	"open-cluster-management.io/governance-policy-propagator/test/utils"

	fwutils "open-cluster-management.io/governance-policy-framework-addon/controllers/utils"
)

func generateEvent(ctx context.Context, plcName string, cfgPlcName string, msg string, complianceState string) {
	managedPlc := utils.GetWithTimeout(
		clientManagedDynamic,
		gvrPolicy,
		plcName,
		clusterNamespace,
		true,
		defaultTimeoutSeconds,
	)
	Expect(managedPlc).NotTo(BeNil())

	configPlc := utils.GetWithTimeout(
		clientManagedDynamic,
		gvrConfigurationPolicy,
		cfgPlcName,
		clusterNamespace,
		true,
		defaultTimeoutSeconds,
	)
	Expect(configPlc).NotTo(BeNil())

	err := managedEventSender.SendEvent(
		ctx,
		configPlc,
		metav1.OwnerReference{
			APIVersion: managedPlc.GetAPIVersion(),
			Kind:       managedPlc.GetKind(),
			Name:       managedPlc.GetName(),
			UID:        managedPlc.GetUID(),
		},
		fwutils.EventReason(clusterNamespace, cfgPlcName),
		msg,
		policiesv1.ComplianceState(complianceState),
	)
	Expect(err).ToNot(HaveOccurred())
}

var _ = Describe("Compliance API recording", Ordered, Label("compliance-events-api"), func() {
	const yamlPath = "../resources/case23_compliance_api_recording/policy.yaml"
	const yamlPath2 = "../resources/case23_compliance_api_recording/policy2.yaml"
	var server *http.Server
	lock := sync.RWMutex{}
	requests := []*complianceeventsapi.ComplianceEvent{}

	BeforeAll(func(ctx context.Context) {
		mux := http.NewServeMux()

		complianceAPIURL := os.Getenv("COMPLIANCE_API_URL")
		Expect(complianceAPIURL).ToNot(BeEmpty())

		parsedURL, err := url.Parse(complianceAPIURL)
		Expect(err).ToNot(HaveOccurred())

		server = &http.Server{
			Addr:    parsedURL.Host,
			Handler: mux,
		}

		mux.HandleFunc("/api/v1/compliance-events", func(w http.ResponseWriter, r *http.Request) {
			r.Header.Set("Content-Type", "application/json")
			if r.Method != http.MethodPost {
				w.WriteHeader(http.StatusMethodNotAllowed)
				_, _ = w.Write([]byte(`{"message": "Invalid method"}`))

				return
			}

			if r.Header.Get("Authorization") == "" {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"message": "No token sent"}`))

				return
			}

			body, err := io.ReadAll(r.Body)
			if err != nil {
				log.Error(err, "error reading request body")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"message": "Could not read request body"}`))

				return
			}

			reqEvent := &complianceeventsapi.ComplianceEvent{}

			if err := json.Unmarshal(body, reqEvent); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"message": "Invalid JSON"}`))

				return
			}

			if err := reqEvent.Cluster.Validate(); err != nil {
				log.Error(err, "Invalid cluster")

				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"message": "Invalid cluster"}`))

				return
			}

			if err := reqEvent.Event.Validate(); err != nil {
				log.Error(err, "Invalid event")

				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"message": "Invalid event"}`))

				return
			}

			if reqEvent.Policy.KeyID == 0 {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"message": "Invalid policy ID"}`))

				return
			}

			w.WriteHeader(http.StatusCreated)

			lock.Lock()
			requests = append(requests, reqEvent)
			lock.Unlock()
		})

		go func() {
			defer GinkgoRecover()

			err := server.ListenAndServe()
			Expect(err).To(MatchError(http.ErrServerClosed))
		}()
	})

	AfterAll(func(ctx context.Context) {
		err := server.Shutdown(ctx)
		Expect(err).ToNot(HaveOccurred())

		By("Deleting a policy on hub cluster in ns:" + clusterNamespaceOnHub)
		_, err = kubectlHub("delete", "-f", yamlPath, "-n", clusterNamespaceOnHub, "--ignore-not-found")
		Expect(err).ToNot(HaveOccurred())
		opt := metav1.ListOptions{}
		utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 0, true, defaultTimeoutSeconds)
		utils.ListWithTimeout(clientManagedDynamic, gvrPolicy, opt, 0, true, defaultTimeoutSeconds)

		By("clean up all events")
		_, err = kubectlManaged("delete", "events", "-n", clusterNamespace, "--all")
		Expect(err).ShouldNot(HaveOccurred())
	})

	AfterEach(func() {
		lock.Lock()
		defer lock.Unlock()

		requests = []*complianceeventsapi.ComplianceEvent{}
	})

	It("Forwards compliance events from a controller to the compliance API", func(ctx context.Context) {
		hubApplyPolicy("case23", yamlPath)

		By("Generates a Pending message")
		generateEvent(ctx, "case23", "case23", "Halt, who goes there?", "Pending")
		By("Generates a NonCompliant message")
		generateEvent(ctx, "case23", "case23", "You shall not pass", "NonCompliant")
		By("Generates a Compliant message")
		generateEvent(ctx, "case23", "case23", "You may pass", "Compliant")

		By("Waiting for the compliance API requests to come in")
		Eventually(
			func(g Gomega) {
				lock.RLock()
				defer lock.RUnlock()

				g.Expect(requests).To(HaveLen(3))

				g.Expect(requests[0].Event.Compliance).To(Equal("Pending"))
				g.Expect(requests[0].Event.Message).To(Equal("Halt, who goes there?"))
				g.Expect(requests[0].ParentPolicy.KeyID).To(BeEquivalentTo(1))
				g.Expect(requests[0].Policy.KeyID).To(BeEquivalentTo(3))

				g.Expect(requests[1].Event.Compliance).To(Equal("NonCompliant"))
				g.Expect(requests[1].Event.Message).To(Equal("You shall not pass"))
				g.Expect(requests[1].ParentPolicy.KeyID).To(BeEquivalentTo(1))
				g.Expect(requests[1].Policy.KeyID).To(BeEquivalentTo(3))

				g.Expect(requests[2].Event.Compliance).To(Equal("Compliant"))
				g.Expect(requests[2].Event.Message).To(Equal("You may pass"))
				g.Expect(requests[2].ParentPolicy.KeyID).To(BeEquivalentTo(1))
				g.Expect(requests[2].Policy.KeyID).To(BeEquivalentTo(3))
			},
			defaultTimeoutSeconds,
			1,
		).Should(Succeed())
	})

	It("Forwards a disabled compliance event to the compliance API", func(ctx context.Context) {
		By("Renaming the configuration policy in the parent policy")
		hubApplyPolicy("case23", yamlPath2)

		By("Waiting for the compliance API request to come in")
		Eventually(
			func(g Gomega) {
				lock.RLock()
				defer lock.RUnlock()

				g.Expect(requests).To(HaveLen(1))

				g.Expect(requests[0].Event.Compliance).To(Equal("Disabled"))
				g.Expect(requests[0].Event.Message).To(Equal("The policy was removed from the parent policy"))
				g.Expect(requests[0].ParentPolicy.KeyID).To(BeEquivalentTo(1))
				g.Expect(requests[0].Policy.KeyID).To(BeEquivalentTo(3))
			},
			defaultTimeoutSeconds,
			1,
		).Should(Succeed())
	})

	It("Forwards a disabled compliance event to the compliance API", func(ctx context.Context) {
		By("Deleting the parent policy")
		_, err := kubectlHub("delete", "-f", yamlPath2, "-n", clusterNamespaceOnHub)
		Expect(err).ToNot(HaveOccurred())

		By("Waiting for the compliance API request to come in")
		Eventually(
			func(g Gomega) {
				lock.RLock()
				defer lock.RUnlock()

				g.Expect(requests).To(HaveLen(1))

				g.Expect(requests[0].Event.Compliance).To(Equal("Disabled"))
				g.Expect(requests[0].Event.Message).To(Equal(
					"The policy was removed because the parent policy no longer applies to this cluster",
				))
				g.Expect(requests[0].ParentPolicy.KeyID).To(BeEquivalentTo(1))
				g.Expect(requests[0].Policy.KeyID).To(BeEquivalentTo(4))
			},
			defaultTimeoutSeconds,
			1,
		).Should(Succeed())
	})
})
