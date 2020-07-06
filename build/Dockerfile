FROM registry.access.redhat.com/ubi8/ubi-minimal:latest

ENV OPERATOR=/usr/local/bin/governance-policy-template-sync \
    USER_UID=1001 \
    USER_NAME=governance-policy-template-sync

# install operator binary
COPY build/_output/bin/governance-policy-template-sync ${OPERATOR}

COPY build/bin /usr/local/bin
RUN  /usr/local/bin/user_setup

ENTRYPOINT ["/usr/local/bin/entrypoint"]

RUN microdnf update && \
    microdnf clean all

USER ${USER_UID}
