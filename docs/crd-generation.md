# CRD Generation

CRDs within the Knative projects are not currently generated, but must be managed manually. This issue is currently
being tracked in [github.com/knative/hack/issues/52](https://github.com/knative/hack/issues/52).

There is a schema tool within [cmd/schema](../cmd/schema) which will generate an OpenAPI specification from the CRD
go-types, however it's the responsibility of the developer to verify this and manually update the CRD yaml manifests.

For example, to update the `NatsJetstreamChannel` CRD:

```sh
go run ./cmd/schema dump NatsJetstreamChannel > natsjetstreamchannel.yaml
```

You then need to copy the contents of `natsjetstreamchannel.yaml` into the
property: `spec.versions[].schema.openAPIV3Schema`, within the correct item of `spec.version[]` which you're editing.

> There's a common issue where `.status.address` is marked as a required field in the schema generation, you will have
> to manually remove the `.status.required` array to fix this.
