package fetch

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/url"
	"syscall"
)

// Explain turns a failed request's error into a message a reader can act
// on, keeping the original error wrapped. Errors it doesn't recognise are
// returned unchanged.
func Explain(err error) error {
	if err == nil {
		return nil
	}
	var host string
	var ue *url.Error
	if errors.As(err, &ue) {
		if u, perr := url.Parse(ue.URL); perr == nil {
			host = u.Hostname()
		}
	}

	var dnsErr *net.DNSError
	var certErr *tls.CertificateVerificationError
	var unknownAuthority x509.UnknownAuthorityError
	var hostnameErr x509.HostnameError
	var invalidCert x509.CertificateInvalidError
	switch {
	case errors.As(err, &dnsErr):
		return &explained{fmt.Sprintf("can't find the server %s; check the address and your internet connection", dnsErr.Name), err}
	case errors.Is(err, syscall.ECONNREFUSED):
		return &explained{fmt.Sprintf("%s refused the connection", orServer(host)), err}
	case errors.As(err, &certErr), errors.As(err, &unknownAuthority), errors.As(err, &hostnameErr), errors.As(err, &invalidCert):
		return &explained{fmt.Sprintf("%s's security certificate isn't valid, so the connection isn't safe", orServer(host)), err}
	case isTimeout(err):
		return &explained{fmt.Sprintf("%s took too long to respond", orServer(host)), err}
	}
	return err
}

func isTimeout(err error) bool {
	var t interface{ Timeout() bool }
	return errors.As(err, &t) && t.Timeout()
}

func orServer(host string) string {
	if host == "" {
		return "the server"
	}
	return host
}

// explained is an error with a friendlier message that still unwraps to
// the original, so errors.Is and errors.As keep working.
type explained struct {
	msg string
	err error
}

func (e *explained) Error() string { return e.msg }
func (e *explained) Unwrap() error { return e.err }
