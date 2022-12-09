package templatesync

import (
	"errors"

	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	policyUserErrorsCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "policy_user_errors_total",
			Help: "The number of user errors encountered while processing policies",
		},
		[]string{
			"policy",
			"template",
			"type",
		},
	)
	policySystemErrorsCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "policy_system_errors_total",
			Help: "The number of system errors encountered while processing policies",
		},
		[]string{
			"policy",
			"template",
			"type",
		},
	)
)

func init() {
	// Register custom metrics with the global Prometheus registry
	// Error metrics may already be registered by another controller
	alreadyReg := &prometheus.AlreadyRegisteredError{}

	regErr := metrics.Registry.Register(policySystemErrorsCounter)
	if regErr != nil && !errors.As(regErr, alreadyReg) {
		panic(regErr)
	}

	regErr = metrics.Registry.Register(policyUserErrorsCounter)
	if regErr != nil && !errors.As(regErr, alreadyReg) {
		panic(regErr)
	}
}
