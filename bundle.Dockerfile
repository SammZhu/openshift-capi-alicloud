FROM scratch

# Core OLM bundle labels
LABEL operators.operatorframework.io.bundle.mediatype.v1=registry+v1
LABEL operators.operatorframework.io.bundle.manifests.v1=manifests/
LABEL operators.operatorframework.io.bundle.metadata.v1=metadata/
LABEL operators.operatorframework.io.bundle.package.v1=cluster-api-provider-alibabacloud
LABEL operators.operatorframework.io.bundle.channels.v1=alpha
LABEL operators.operatorframework.io.bundle.channel.default.v1=alpha
# OpenShift version range (community-operators-prod)
LABEL com.redhat.openshift.versions="v4.16-v4.20"

COPY manifests /manifests/
COPY metadata /metadata/
