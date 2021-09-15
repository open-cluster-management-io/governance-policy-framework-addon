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
	github.com/Microsoft/hcsshim => github.com/Microsoft/hcsshim v0.8.22
	github.com/coreos/etcd => go.etcd.io/etcd v3.3.24+incompatible
	github.com/dgrijalva/jwt-go => github.com/golang-jwt/jwt v3.2.2+incompatible
	github.com/go-logr/logr => github.com/go-logr/logr v0.2.1
	github.com/go-logr/zapr => github.com/go-logr/zapr v0.2.0
	github.com/gogo/protobuf => github.com/gogo/protobuf v1.3.2
	github.com/gorilla/websocket => github.com/gorilla/websocket v1.4.2
	github.com/hashicorp/consul => github.com/hashicorp/consul v1.10.2
	github.com/influxdata/influxdb => github.com/influxdata/influxdb v1.8.9
	github.com/opencontainers/runc => github.com/opencontainers/runc v1.0.2
	github.com/openshift/origin => github.com/openshift/origin v1.5.1
	github.com/prometheus-operator/prometheus-operator => github.com/prometheus-operator/prometheus-operator v0.50.0
	github.com/prometheus/prometheus => github.com/prometheus/prometheus v2.7.1+incompatible
	github.com/spf13/viper => github.com/spf13/viper v1.8.1
	k8s.io/client-go => k8s.io/client-go v0.20.5
	k8s.io/kubernetes => k8s.io/kubernetes v1.20.5
)
