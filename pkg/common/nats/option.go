package nats

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"

	"github.com/nats-io/nats.go"
	v1 "k8s.io/api/core/v1"
)

const (
	TLSCaCertKey = "ca.crt"
)

var (
	ErrTLSCertMissing       = fmt.Errorf("%s missing", v1.TLSCertKey)
	ErrTLSPrivateKeyMissing = fmt.Errorf("%s missing", v1.TLSPrivateKeyKey)
	ErrTLSCaCertMissing     = fmt.Errorf("%s missing", TLSCaCertKey)
	ErrTLSSecretNotValid    = fmt.Errorf("TLS secret not of type %s", v1.SecretTypeTLS)
)

// ClientCert is similar to nats.ClientCert() except it accepts the data from a v1.Secret instead of reading from the
// file system. Passing a nil secret to this option is a no-op.
func ClientCert(secret *v1.Secret) nats.Option {
	return func(o *nats.Options) error {
		if secret == nil {
			return nil
		}

		if secret.Type != v1.SecretTypeTLS {
			return ErrTLSSecretNotValid
		}

		certKey, ok := secret.Data[v1.TLSCertKey]
		if !ok {
			return ErrTLSCertMissing
		}

		privateKey, ok := secret.Data[v1.TLSPrivateKeyKey]
		if !ok {
			return ErrTLSPrivateKeyMissing
		}

		cert, err := tls.X509KeyPair(certKey, privateKey)
		if err != nil {
			return fmt.Errorf("error loading client certificate: %v", err)
		}

		cert.Leaf, err = x509.ParseCertificate(cert.Certificate[0])
		if err != nil {
			return fmt.Errorf("error parsing client certificate: %v", err)
		}

		if o.TLSConfig == nil {
			o.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
		}

		o.TLSConfig.Certificates = []tls.Certificate{cert}

		if caCert, ok := secret.Data[TLSCaCertKey]; ok {
			if o.TLSConfig.RootCAs == nil {
				o.TLSConfig.RootCAs = x509.NewCertPool()
			}

			if !o.TLSConfig.RootCAs.AppendCertsFromPEM(caCert) {
				return fmt.Errorf("failed to parse root certificate from secret")
			}
		}

		return nil
	}
}

// RootCA is similar to nats.RootCAs() except it accepts a certificate directly, instead of reading from the file
// system.
func RootCA(caBundle []byte) nats.Option {
	return func(o *nats.Options) error {
		if o.TLSConfig == nil {
			o.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
		}

		if o.TLSConfig.RootCAs == nil {
			o.TLSConfig.RootCAs = x509.NewCertPool()
		}

		if !o.TLSConfig.RootCAs.AppendCertsFromPEM(caBundle) {
			return fmt.Errorf("failed to parse root certificate from caBundle")
		}

		return nil
	}
}
