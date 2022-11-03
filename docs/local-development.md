# Local Development

This document aims to aid contributors for running this package in a local [k3d][k3d] cluster. If developing in a remote
cluster, it should be as simple as configuring `KO_DOCKER_REPO` to point to a registry of your choosing, however when 
running a k3d cluster in tandem with ko builds, the process is a little more convoluted.

Pre-requisites:

- [ko][ko]
- [k3d][k3d]

Create a cluster with a dedicated registry and traefik disabled (knative requires its own networking implementation 
rather than using traefik - we use kourier for local dev) 

```sh
k3d cluster create knative \
    --api-port 6550 \
    --servers 1 \
    --image rancher/k3s:v1.21.9-k3s1 \
    --port 80:80@loadbalancer \
    --port 443:443@loadbalancer \
    --k3s-arg '--no-deploy=traefik@server:*' \
    --registry-create k3d-ko.local:12345 \
    --wait --verbose
```

Deploy Knative eventing to your cluster, following the getting started guide of your choosing from 
[Installing Knative][install-knative].

> Depending on your OS, you need to ensure `k3d-ko.local` resolves to `127.0.0.1`. If your OS does not do this by 
> default, the simplest method is to add a line to `/etc/hosts`:
> 
> ```
> 127.0.0.1 k3d-ko.local
> ```

Deploy eventing-natss to your cluster (ensuring your `kubectl` context is configured to your local cluster):

```sh
export KO_DOCKER_REPO=k3d-ko.local:12345
ko apply -f config/webhook
ko apply -f config/jetstream
```

[k3d]: https://k3d.io/v5.3.0/
[ko]: https://github.com/google/ko
[install-knative]: https://knative.dev/docs/install/
