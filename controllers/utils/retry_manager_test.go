// Copyright Contributors to the Open Cluster Management project

package utils

import (
	"errors"
	"fmt"
	"io"
	"net"
	"syscall"
	"testing"
)

func TestIsRetryableManagerStartError(t *testing.T) {
	t.Parallel()

	dnsErr := &net.DNSError{
		Err:  "connection refused",
		Name: "api.example.hub",
	}

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "nil",
			err:  nil,
			want: false,
		},
		{
			name: "non-retryable",
			err:  errors.New("unable to set up health check"),
			want: false,
		},
		{
			name: "dns error",
			err:  dnsErr,
			want: true,
		},
		{
			name: "wrapped dns error matching ACM-34590",
			err: fmt.Errorf(
				"failed to determine if *v1.Secret is namespaced: failed to get restmapping: "+
					"failed to get server groups: Get \"https://api.example:6443/api\": %w",
				dnsErr,
			),
			want: true,
		},
		{
			name: "non-retryable op error",
			err: &net.OpError{
				Op:  "dial",
				Net: "unix",
				Err: errors.New("invalid argument"),
			},
			want: false,
		},
		{
			name: "connection refused errno",
			err: &net.OpError{
				Op:  "dial",
				Net: "tcp",
				Err: syscall.ECONNREFUSED,
			},
			want: true,
		},
		{
			name: "wrapped connection refused errno",
			err: fmt.Errorf(
				"failed to get server groups: Get \"https://api.example:6443/api\": %w",
				&net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED},
			),
			want: true,
		},
		{
			name: "connection reset errno",
			err:  syscall.ECONNRESET,
			want: true,
		},
		{
			name: "timed out errno",
			err:  syscall.ETIMEDOUT,
			want: true,
		},
		{
			name: "network unreachable errno",
			err:  syscall.ENETUNREACH,
			want: true,
		},
		{
			name: "host unreachable errno",
			err:  syscall.EHOSTUNREACH,
			want: true,
		},
		{
			name: "unexpected eof",
			err:  fmt.Errorf("read response: %w", io.ErrUnexpectedEOF),
			want: true,
		},
		{
			name: "tls handshake timeout string",
			err:  errors.New("net/http: TLS handshake timeout"),
			want: true,
		},
		{
			name: "client connection lost string",
			err:  errors.New("http2: client connection lost"),
			want: true,
		},
		{
			name: "connection refused string only is not enough",
			err:  errors.New("something failed: connection refused"),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := isRetryableManagerStartError(tt.err); got != tt.want {
				t.Fatalf("isRetryableManagerStartError() = %v, want %v (err=%v)", got, tt.want, tt.err)
			}
		})
	}
}
