module github.com/open-cluster-management/governance-policy-spec-sync

go 1.16

require (
	github.com/onsi/ginkgo v1.16.5
	github.com/onsi/gomega v1.16.0
	github.com/open-cluster-management/governance-policy-propagator v0.0.0-20211012174109-95c3b77cce09
	github.com/open-cluster-management/klusterlet-addon-controller v0.0.0-20210303215539-1d12cebe6f19
	github.com/open-cluster-management/multicloud-operators-placementrule v1.2.4-0-20210816-699e5
	github.com/spf13/pflag v1.0.5
	k8s.io/api v0.21.3
	k8s.io/apimachinery v0.21.3
	k8s.io/client-go v12.0.0+incompatible
	k8s.io/klog v1.0.0
	sigs.k8s.io/controller-runtime v0.9.2
)

replace (
	github.com/go-logr/logr v0.1.0 => github.com/go-logr/logr v0.2.1
	github.com/go-logr/logr v0.2.0 => github.com/go-logr/logr v0.2.1
	github.com/go-logr/zapr => github.com/go-logr/zapr v0.4.0
	github.com/gogo/protobuf => github.com/gogo/protobuf v1.3.2
	k8s.io/client-go => k8s.io/client-go v0.21.3
)
