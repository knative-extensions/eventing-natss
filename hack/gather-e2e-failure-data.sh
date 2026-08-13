#!/usr/bin/env bash

# Copyright 2026 The Knative Authors
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# Diagnostics must never hide the original test failure because one resource,
# CRD, pod, or previous container instance is already gone.
set +e

OUT_DIR="${OUT_DIR:-diagnostics}"
LOG_TAIL_LINES="${LOG_TAIL_LINES:-500}"
LOG_LIMIT_BYTES="${LOG_LIMIT_BYTES:-1048576}"
KUBECTL_COMMAND_TIMEOUT="${KUBECTL_COMMAND_TIMEOUT:-30s}"
KUBECTL_KILL_AFTER="${KUBECTL_KILL_AFTER:-5s}"
KUBECTL_REQUEST_TIMEOUT="${KUBECTL_REQUEST_TIMEOUT:-20s}"
mkdir -p "${OUT_DIR}"

# Bound both the client request and the process itself. The process timeout is
# still needed when kubectl hangs before it can enforce its request deadline.
kubectl_bounded() {
  timeout --kill-after="${KUBECTL_KILL_AFTER}" "${KUBECTL_COMMAND_TIMEOUT}" \
    kubectl --request-timeout="${KUBECTL_REQUEST_TIMEOUT}" "$@"
}

capture() {
  local file="$1"
  shift
  mkdir -p "$(dirname "${file}")"
  {
    printf '$'
    printf ' %q' "$@"
    printf '\n'
    "$@"
  } >"${file}" 2>&1 || true
}

capture "${OUT_DIR}/brokers.yaml" kubectl_bounded get brokers --all-namespaces=true -o yaml
capture "${OUT_DIR}/channels.yaml" kubectl_bounded get channels --all-namespaces=true -o yaml
capture "${OUT_DIR}/natsjetstreamchannels.yaml" kubectl_bounded get natsjetstreamchannels.messaging.knative.dev --all-namespaces=true -o yaml
capture "${OUT_DIR}/triggers.yaml" kubectl_bounded get triggers --all-namespaces=true -o yaml
capture "${OUT_DIR}/scaledobjects.yaml" kubectl_bounded get scaledobjects.keda.sh --all-namespaces=true -o yaml
capture "${OUT_DIR}/hpas.yaml" kubectl_bounded get horizontalpodautoscalers.autoscaling --all-namespaces=true -o yaml
capture "${OUT_DIR}/events.yaml" kubectl_bounded get events --all-namespaces=true -o yaml

# The NATS namespace is included with the two requested control-plane
# namespaces because broker symptoms are otherwise disconnected from the
# backing JetStream server logs.
namespaces=(knative-eventing keda nats-io)
while IFS= read -r namespace; do
  if [[ -n "${namespace}" ]]; then
    namespaces+=("${namespace}")
  fi
done < <(kubectl_bounded get namespaces \
  -l app.kubernetes.io/component=reconciler-test \
  -o 'jsonpath={range .items[*]}{.metadata.name}{"\n"}{end}' 2>/dev/null || true)

declare -A seen_namespaces=()
for namespace in "${namespaces[@]}"; do
  if [[ -n "${seen_namespaces[${namespace}]:-}" ]]; then
    continue
  fi
  seen_namespaces["${namespace}"]=1

  namespace_dir="${OUT_DIR}/namespaces/${namespace}"
  mkdir -p "${namespace_dir}/logs"

  # `describe` is intentionally forbidden: it prints literal environment
  # values from Pod templates. Wide tables contain workload identity, image,
  # placement, and status without dumping env or Secret values.
  capture "${namespace_dir}/pods-wide.txt" kubectl_bounded get pods -n "${namespace}" -o wide
  capture "${namespace_dir}/deployments-wide.txt" kubectl_bounded get deployments -n "${namespace}" -o wide
  capture "${namespace_dir}/jobs-wide.txt" kubectl_bounded get jobs -n "${namespace}" -o wide

  while IFS= read -r pod_ref; do
    if [[ -z "${pod_ref}" ]]; then
      continue
    fi
    pod="${pod_ref#pod/}"
    containers="$(kubectl_bounded get pod "${pod}" -n "${namespace}" \
      -o 'jsonpath={range .spec.initContainers[*]}{.name}{" "}{end}{range .spec.containers[*]}{.name}{" "}{end}{range .spec.ephemeralContainers[*]}{.name}{" "}{end}' 2>/dev/null || true)"
    for container in ${containers}; do
      capture "${namespace_dir}/logs/${pod}-${container}-current.log" \
        kubectl_bounded logs -n "${namespace}" "${pod}" -c "${container}" \
        --timestamps=true --tail="${LOG_TAIL_LINES}" --limit-bytes="${LOG_LIMIT_BYTES}"
      capture "${namespace_dir}/logs/${pod}-${container}-previous.log" \
        kubectl_bounded logs -n "${namespace}" "${pod}" -c "${container}" \
        --timestamps=true --tail="${LOG_TAIL_LINES}" --limit-bytes="${LOG_LIMIT_BYTES}" --previous
    done
  done < <(kubectl_bounded get pods -n "${namespace}" -o name 2>/dev/null || true)
done

# Do not add `kubectl get/describe secrets` or any `kubectl describe` here.
# Secret and literal Pod environment values are deliberately excluded.
exit 0
