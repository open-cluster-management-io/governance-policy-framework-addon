package apis

import (
	policiesv1 "github.com/open-cluster-management/governance-policy-propagator/pkg/apis/policies/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// AddToSchemes may be used to add all resources defined in the project to a Scheme
var AddToSchemes runtime.SchemeBuilder

// AddToScheme adds all Resources to the Scheme
func AddToScheme(s *runtime.Scheme) error {
	// add policy scheme
	if err := policiesv1.SchemeBuilder.AddToScheme(s); err != nil {
		return err
	}
	return AddToSchemes.AddToScheme(s)
}
