FROM debian@sha256:a99cfc517144bc59b1978475ec53b46ecabec7e43635402ee5b77cc54cd1b20a
RUN apt-get update && apt-get install -y --no-install-recommends \
    python3 util-linux fdisk e2fsprogs dosfstools xz-utils ca-certificates \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /build
ENTRYPOINT ["python3", "/source/scripts/image/build.py"]
