FROM --platform=linux/amd64 ubuntu
RUN apt update && \
    apt install -y ca-certificates wget && \
    apt clean
COPY docker-entrypoint.sh /
ENV LAMBDAFY_VERSION=0.0.1
ENTRYPOINT ["/docker-entrypoint.sh"]
