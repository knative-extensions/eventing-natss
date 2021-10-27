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
	"fmt"
	"k8s.io/apimachinery/pkg/util/yaml"
	commonconfig "knative.dev/eventing-natss/pkg/common/config"
	"knative.dev/eventing-natss/pkg/common/constants"
)

func LoadEventingNatsConfig(configMap map[string]string) (config commonconfig.EventingNatsConfig, err error) {
	eventingNats, ok := configMap[constants.EventingNatsSettingsConfigKey]
	if !ok {
		return config, fmt.Errorf("missing configmap entry: %s", constants.EventingNatsSettingsConfigKey)
	}

	return config, yaml.Unmarshal([]byte(eventingNats), &config)
}
