/*
Copyright 2020 The Knative Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package direct

import (
	"context"
	"embed"

	"knative.dev/reconciler-test/pkg/environment"
	"knative.dev/reconciler-test/pkg/feature"
	"knative.dev/reconciler-test/pkg/manifest"
)

//go:embed *.yaml
var yamls embed.FS

func Install() feature.StepFn {
	return func(ctx context.Context, t feature.T) {
		registerImages(ctx, t)
		args := map[string]interface{}{"producerCount": 5}
		if _, err := manifest.InstallYamlFS(ctx, yamls, args); err != nil {
			t.Fatal(err)
		}
	}
}

func registerImages(ctx context.Context, t feature.T) {
	opt := environment.RegisterPackage(manifest.ImagesFromFS(ctx, yamls)...)
	if _, err := opt(ctx, nil); err != nil {
		t.Fatal(err)
	}
}
