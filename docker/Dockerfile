FROM alpine:latest as certs
RUN apk --update add ca-certificates

FROM scratch
COPY --from=certs /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
ADD istio-registry-sync-static /usr/bin/istio-registry-sync
ENTRYPOINT ["/usr/bin/istio-registry-sync"]
