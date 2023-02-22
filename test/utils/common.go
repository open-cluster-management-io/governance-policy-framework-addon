// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package utils

import (
	"os/exec"
	"strings"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	corev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/record"
)

var (
	GkVersion    = "v3.11.0"
	GkDeployment = "https://raw.githubusercontent.com/open-policy-agent/gatekeeper/" +
		GkVersion + "/deploy/gatekeeper.yaml"
)

// CreateRecorder return recorder
func CreateRecorder(kubeClient kubernetes.Interface, componentName string) (record.EventRecorder, error) {
	eventsScheme := runtime.NewScheme()
	if err := v1.AddToScheme(eventsScheme); err != nil {
		return nil, err
	}

	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartRecordingToSink(&corev1.EventSinkImpl{Interface: kubeClient.CoreV1().Events("")})

	return eventBroadcaster.NewRecorder(eventsScheme, v1.EventSource{Component: componentName}), nil
}

// getMetrics curls the metrics endpoint, filters the response with the given patterns,
// and returns the value(s) for the matching metric(s).
func GetMetrics(metricPatterns ...string) []string {
	metricFilter := " | grep " + strings.Join(metricPatterns, " | grep ")
	metricsCmd := `curl localhost:8383/metrics` + metricFilter
	cmd := exec.Command("bash", "-c", metricsCmd)

	matchingMetricsRaw, err := cmd.Output()
	if err != nil {
		if err.Error() == "exit status 1" {
			return []string{} // exit 1 indicates that grep couldn't find a match.
		}

		return []string{err.Error()}
	}

	matchingMetrics := strings.Split(strings.TrimSpace(string(matchingMetricsRaw)), "\n")
	values := make([]string, len(matchingMetrics))

	for i, metric := range matchingMetrics {
		fields := strings.Fields(metric)
		if len(fields) > 0 {
			values[i] = fields[len(fields)-1]
		}
	}

	return values
}
