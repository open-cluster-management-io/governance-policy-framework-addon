module github.com/open-cluster-management/governance-policy-spec-sync

go 1.16

require (
	github.com/onsi/ginkgo v1.16.5
	github.com/onsi/gomega v1.16.0
	github.com/open-cluster-management/governance-policy-propagator v0.0.0-20211012174109-95c3b77cce09
	github.com/spf13/pflag v1.0.5
	k8s.io/api v0.22.1
	k8s.io/apimachinery v0.22.1
	k8s.io/client-go v12.0.0+incompatible
	k8s.io/klog v1.0.0
	open-cluster-management.io/addon-framework v0.1.0
	sigs.k8s.io/controller-runtime v0.9.2
)

replace (
	github.com/go-logr/zapr => github.com/go-logr/zapr v0.4.0
	github.com/open-cluster-management/multicloud-operators-placementrule => github.com/open-cluster-management/multicloud-operators-placementrule v1.2.4-0-20210816-699e5.0.20211012154812-5fac6c25d2f6
	k8s.io/client-go => k8s.io/client-go v0.22.1
)
