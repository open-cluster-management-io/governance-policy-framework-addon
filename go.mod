module github.com/open-cluster-management/governance-policy-template-sync

go 1.16

require (
	github.com/onsi/ginkgo v1.14.1
	github.com/onsi/gomega v1.10.1
	github.com/open-cluster-management/governance-policy-propagator v0.0.0-20210409191610-0ec1d5a4e19d
	github.com/operator-framework/operator-sdk v0.19.4
	github.com/spf13/pflag v1.0.5
	k8s.io/api v0.20.5
	k8s.io/apimachinery v0.20.5
	k8s.io/client-go v12.0.0+incompatible
	k8s.io/klog v1.0.0
	sigs.k8s.io/controller-runtime v0.6.2
)

replace (
	github.com/go-logr/logr => github.com/go-logr/logr v0.2.1
	github.com/go-logr/zapr => github.com/go-logr/zapr v0.2.0
	github.com/gogo/protobuf => github.com/gogo/protobuf v1.3.2
	k8s.io/client-go => k8s.io/client-go v0.20.5
)
