# The sample needs only the standard library; omit unused OpenEXR image codecs
# inherited by the full Python image (CVE-2026-42216).
# Certificates
FROM --platform=$BUILDPLATFORM debian:bookworm-slim AS certs
# This line busts the cache if there's security updates.
ADD https://deb.debian.org/debian/dists/bookworm/InRelease /tmp/bust-debian
ADD https://deb.debian.org/debian/dists/bookworm-updates/InRelease /tmp/bust-updates
ADD https://security.debian.org/debian-security/dists/bookworm-security/InRelease /tmp/bust-security
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates

FROM python:3-slim-trixie
# This line busts the cache if there's security updates.
ADD https://deb.debian.org/debian/dists/bookworm/InRelease /tmp/bust-debian
ADD https://deb.debian.org/debian/dists/bookworm-updates/InRelease /tmp/bust-updates
ADD https://security.debian.org/debian-security/dists/bookworm-security/InRelease /tmp/bust-security
ADD https://deb.debian.org/debian/dists/trixie/InRelease /tmp/bust-trixie
ADD https://deb.debian.org/debian/dists/trixie-updates/InRelease /tmp/bust-trixie-updates
ADD https://security.debian.org/debian-security/dists/trixie-security/InRelease /tmp/bust-trixie-security
COPY server.py /
RUN rm -rf /etc/ssl/certs /usr/share/ca-certificates
COPY --from=certs /usr/share/ca-certificates/ /usr/share/ca-certificates/
COPY --from=certs /etc/ssl/certs/ /etc/ssl/certs/
COPY --from=certs /etc/ca-certificates.conf /etc/ca-certificates.conf
COPY --from=certs /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
ENV SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt SSL_CERT_DIR=/etc/ssl/certs \
    REQUESTS_CA_BUNDLE=/etc/ssl/certs/ca-certificates.crt AWS_CA_BUNDLE=/etc/ssl/certs/ca-certificates.crt
CMD python /server.py
