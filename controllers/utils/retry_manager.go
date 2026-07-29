// Copyright Contributors to the Open Cluster Management project

package utils

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"syscall"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

// NewManagerWithRetry creates a controller-runtime manager, retrying on failure so the
// process does not exit in sub-second on transient startup errors (for example hub DNS
// unavailable during an SNO reboot), which can trigger CRI-O RunContainerError races.
//
// The first failure always triggers one retry after 2s. Further retries (4s, 8s, 16s)
// continue only while the error looks like a transient connectivity/DNS failure.
// Returns the last error if all attempts fail, or if the context is cancelled while waiting.
func NewManagerWithRetry(
	ctx context.Context, log logr.Logger, cfg *rest.Config, options manager.Options,
) (manager.Manager, error) {
	mgr, err := ctrl.NewManager(cfg, options)
	if err == nil {
		return mgr, nil
	}

	for _, wait := range []int{2, 4, 8, 16} {
		log.Error(err, "Failed to create manager; will retry", "waitSeconds", wait)

		timer := time.NewTimer(time.Duration(wait) * time.Second)

		select {
		case <-ctx.Done():
			timer.Stop()

			return nil, fmt.Errorf("aborted while retrying manager creation: %w", ctx.Err())
		case <-timer.C:
		}

		mgr, err = ctrl.NewManager(cfg, options)
		if err == nil {
			return mgr, nil
		}

		if !isRetryableManagerStartError(err) {
			return nil, err
		}
	}

	// Getting here means the manager had failures every time
	return nil, err
}

// isRetryableManagerStartError reports whether err is a transient reachability failure
// that may clear without process restart (DNS not ready, connection refused, timeouts).
func isRetryableManagerStartError(err error) bool {
	if err == nil {
		return false
	}

	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return true
	}

	var netErr net.Error
	if errors.As(err, &netErr) && (netErr.Timeout() || temporaryNetError(netErr)) {
		return true
	}

	for _, target := range []error{
		syscall.ECONNREFUSED,
		syscall.ECONNRESET,
		syscall.ETIMEDOUT,
		syscall.ENETUNREACH,
		syscall.EHOSTUNREACH,
		io.ErrUnexpectedEOF,
	} {
		if errors.Is(err, target) {
			return true
		}
	}

	// Some TLS/HTTP2 failures do not unwrap to syscall errno or net.DNSError.
	msg := strings.ToLower(err.Error())
	for _, s := range []string{
		"tls handshake timeout",
		"client connection lost",
	} {
		if strings.Contains(msg, s) {
			return true
		}
	}

	return false
}

// temporaryNetError wraps the deprecated Temporary() check so callers stay intentional.
func temporaryNetError(err net.Error) bool {
	type temporary interface {
		Temporary() bool
	}

	t, ok := err.(temporary)

	return ok && t.Temporary()
}
