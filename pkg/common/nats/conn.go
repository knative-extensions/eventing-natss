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

package nats

import (
	"context"
	"encoding/base64"
	"fmt"
	v1 "k8s.io/api/core/v1"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"knative.dev/pkg/injection"
	"knative.dev/pkg/system"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"

	secretinformer "knative.dev/pkg/client/injection/kube/informers/core/v1/secret"

	commonconfig "knative.dev/eventing-natss/pkg/common/config"
	"knative.dev/eventing-natss/pkg/common/constants"
)

func NewNatsConn(ctx context.Context, config commonconfig.EventingNatsConfig) (*nats.Conn, error) {
	url := config.URL
	if url == "" {
		url = constants.DefaultNatsURL
	}

	ns := getNamespace(ctx)
	var opts []nats.Option

	if config.Auth != nil {
		o, err := buildAuthOption(ctx, ns, *config.Auth)
		if err != nil {
			return nil, err
		}

		opts = append(opts, o...)
	}

	if config.RootCA != nil {
		o, err := buildRootCAOption(ctx, ns, *config.RootCA)
		if err != nil {
			return nil, err
		}

		opts = append(opts, o)
	}

	return nats.Connect(url, opts...)
}

func buildAuthOption(ctx context.Context, ns string, config commonconfig.ENConfigAuth) ([]nats.Option, error) {
	opts := make([]nats.Option, 0, 2)
	if config.CredentialFile != nil {
		o, err := buildCredentialFileOption(ctx, ns, *config.CredentialFile)
		if err != nil {
			return nil, err
		}

		opts = append(opts, o)
	}

	if config.TLS != nil {
		o, err := buildClientTLSOption(ctx, ns, *config.TLS)
		if err != nil {
			return nil, err
		}

		opts = append(opts, o)
	}

	return opts, nil
}

func buildCredentialFileOption(ctx context.Context, ns string, config commonconfig.ENConfigAuthCredentialFile) (nats.Option, error) {
	if config.Secret == nil {
		return nil, nil
	}

	secrets := secretinformer.Get(ctx).Lister().Secrets(ns)
	contents, err := loadSecret(*config.Secret, secrets)
	if err != nil {
		return nil, err
	}

	return credentialFileOption(contents), nil
}

func buildClientTLSOption(ctx context.Context, ns string, config commonconfig.ENConfigAuthTLS) (nats.Option, error) {
	if config.Secret == nil {
		return nil, nil
	}

	secret, err := secretinformer.Get(ctx).Lister().Secrets(ns).Get(config.Secret.Name)
	if err != nil {
		return nil, err
	}

	return ClientCert(secret), nil
}

func buildRootCAOption(ctx context.Context, ns string, config commonconfig.ENConfigRootCA) (nats.Option, error) {
	var (
		decoded []byte
		err     error
	)
	if config.CABundle != "" {
		decoded, err = base64.StdEncoding.DecodeString(config.CABundle)
		if err != nil {
			return nil, err
		}
	} else if config.Secret != nil {
		secret, err := secretinformer.Get(ctx).Lister().Secrets(ns).Get(config.Secret.Name)
		if err != nil {
			return nil, err
		}

		var ok bool
		if decoded, ok = secret.Data[TLSCaCertKey]; !ok {
			return nil, ErrTLSCaCertMissing
		}
	}

	return RootCA(decoded), nil
}

func loadSecret(config v1.SecretKeySelector, secrets corev1listers.SecretNamespaceLister) ([]byte, error) {
	secret, err := secrets.Get(config.Name)
	if err != nil {
		return nil, err
	}

	key := constants.DefaultCredentialFileSecretKey
	if config.Key != "" {
		key = config.Key
	}

	encoded, ok := secret.Data[key]
	if !ok {
		return nil, fmt.Errorf("failed to load secret, key does not exist: %s", key)
	}

	decoded := make([]byte, base64.StdEncoding.DecodedLen(len(encoded)))
	n, err := base64.StdEncoding.Decode(decoded, encoded)
	if err != nil {
		return nil, err
	}

	return decoded[:n], nil
}

// credentialFileOption processes the raw credential file contents and returns the nats.Option. This logic has been
// derived from the nats.UserCredentials() function but modified for when the file has already been parsed.
func credentialFileOption(contents []byte) nats.Option {
	userCB := func() (string, error) {
		return nkeys.ParseDecoratedJWT(contents)
	}

	sigCB := func(nonce []byte) ([]byte, error) {
		// nkeys.KeyPair, error
		kp, err := nkeys.ParseDecoratedNKey(contents)
		if err != nil {
			return nil, err
		}
		// Wipe our key on exit.
		defer kp.Wipe()

		return kp.Sign(nonce)
	}

	return nats.UserJWT(userCB, sigCB)
}

func getNamespace(ctx context.Context) string {
	if injection.HasNamespaceScope(ctx) {
		return injection.GetNamespaceScope(ctx)
	}

	return system.Namespace()
}
