FROM --platform=linux/amd64 ubuntu
RUN apt update && \
    apt install -y ca-certificates wget && \
    apt clean
COPY docker-entrypoint.sh /
ENTRYPOINT ["/docker-entrypoint.sh"]
