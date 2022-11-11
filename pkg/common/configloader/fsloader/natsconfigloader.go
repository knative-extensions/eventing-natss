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

package fsloader

import (
	"context"
	"errors"

	"knative.dev/eventing-natss/pkg/common/configloader"
)

// Key is used as the key for associating information with a context.Context.
type Key struct{}

func WithLoader(ctx context.Context, loader configloader.Loader) context.Context {
	return context.WithValue(ctx, Key{}, loader)
}

// Get obtains the configloader.Loader contained in a Context, if present
func Get(ctx context.Context) (configloader.Loader, error) {
	untyped := ctx.Value(Key{})
	if untyped == nil {
		return nil, errors.New("configloader.Loader does not exist in context")
	}
	return untyped.(configloader.Loader), nil
	// TODO: test the above works and use below if not.
	// return untyped.(func(data string) (map[string]string, error)), nil
}
