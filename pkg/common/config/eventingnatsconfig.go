/*
Copyright 2021 The Knative Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    https://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package config

import (
	v1 "k8s.io/api/core/v1"
)

// EventingNatsConfig represents the YAML configuration which can be provided in the `config-nats` ConfigMap under the
// key, "eventing-nats".
type EventingNatsConfig struct {
	URL      string          `json:"url,omitempty"`
	ConnOpts *ConnOpts       `json:"connOpts,omitempty"`
	Auth     *ENConfigAuth   `json:"auth,omitempty"`
	RootCA   *ENConfigRootCA `json:"tls,omitempty"`
}

// ENConfigAuth provides configuration on how the client should authenticate itself to the server.
type ENConfigAuth struct {
	CredentialFile *ENConfigAuthCredentialFile `json:"credentialFile,omitempty"`
	TLS            *ENConfigAuthTLS            `json:"tls,omitempty"`
}

// ENConfigAuthCredentialFile refers to a NATS Credentials File as specified by:
// https://docs.nats.io/using-nats/developer/connecting/creds
type ENConfigAuthCredentialFile struct {
	// Secret is a reference to an existing Secret where a credential file can be found. The default key is,
	// "nats.creds", but can be overridden by the "key" field.
	Secret *v1.SecretKeySelector `json:"secret,omitempty"`
}

// ENConfigAuthTLS is used to provide client certificates for mTLS connections.
type ENConfigAuthTLS struct {
	// Secret is a reference to an existing Secret of type "kubernetes.io/tls".
	Secret *v1.LocalObjectReference `json:"secret,omitempty"`
}

// ENConfigRootCA specifies how to verify TLS connections to the NATS server. This is only required if the NATS server
// is protected with TLS certificates not defined in the system trust store (i.e. self-signed certs) without mTLS
// enabled, or when mTLS is enabled but the CA is not included in the chain specified by ENConfigAuthTLS.Secret
//
// When this is set, only one of "caBundle" or "secret" must be configured.
type ENConfigRootCA struct {
	// CABundle is a base64 PEM encoded CA certificate. This is only required if the NATS server is served over TLS
	// using certificates not trusted by the system trust store, and, if using mTLS, is not present in the certificates
	// provided to ENConfigAuthTLS.Secret.
	CABundle string `json:"caBundle,omitempty"`

	// Secret is a reference to an existing Secret where the controller will extract a certificate by the "ca.crt" key.
	Secret *v1.LocalObjectReference `json:"secret,omitempty"`
}

type ConnOpts struct {
	// MaxReconnects how many attempts to reconnect
	MaxReconnects int `json:"maxReconnects,omitempty"`
	// RetryOnFailedConnect should retry on failed reconnect
	RetryOnFailedConnect bool `json:"retryOnFailedConnect,omitempty"`
	// ReconnectWaitMilliseconds time between reconnects in milliseconds
	ReconnectWaitMilliseconds int `json:"reconnectWaitMilliseconds,omitempty"`
}
