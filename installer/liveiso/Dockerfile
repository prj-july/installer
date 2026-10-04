FROM debian:bookworm-slim

ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update && apt-get install -y --no-install-recommends \
    live-build xorriso isolinux syslinux-utils \
    curl ca-certificates git rsync \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /build
COPY assets /build/assets
COPY gui /build/gui
COPY build.sh /build/build.sh
RUN chmod +x /build/build.sh

CMD ["/build/build.sh"]
