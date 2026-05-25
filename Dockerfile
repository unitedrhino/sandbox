FROM golang:1.26.0 AS builder

ENV GOPROXY=https://goproxy.cn,https://proxy.golang.org,direct
ENV GOSUMDB=sum.golang.google.cn

# 编译 QuickJS（通过 Gitee 镜像，静态链接避免 glibc 版本问题）
RUN git clone --depth 1 https://gitee.com/mirrors/quickjs.git /tmp/quickjs \
    && make -C /tmp/quickjs -j$(nproc) \
    && cd /tmp/quickjs \
    && gcc -static -O2 -flto -rdynamic -o qjs .obj/qjs.o .obj/repl.o .obj/quickjs.o .obj/libregexp.o .obj/libunicode.o .obj/cutils.o .obj/quickjs-libc.o .obj/libbf.o .obj/qjscalc.o -lm -ldl -lpthread \
    && mkdir -p /out \
    && cp /tmp/quickjs/qjs /out/qjs

WORKDIR /src
COPY backend/.swagger ./.swagger
COPY backend/share ./share
COPY .gits/sandbox ./sandbox
COPY .gits/cli ./cli/ur
# 丢弃 CLI 工作树中的未提交变更，确保使用干净的已提交版本
RUN cd /src/cli/ur && git reset --hard HEAD && git clean -fd
WORKDIR /src/sandbox
RUN go build -ldflags="-s -w" -o /out/sandbox .
WORKDIR /src/cli/ur
RUN go build -ldflags="-s -w" -o /out/ur .

RUN echo "[sandbox] patching package-skill.sh for unified ur binary..." \
    && sed -i \
    -e 's|if ! (cd "${ROOT}" && GOOS="${goos}" GOARCH="${goarch}" go build -o "${bin_dir}/ur-${app}${exe_suffix}" "./cmd/ur-${app}")|if false|g' \
    -e 's|if ! (cd "${ROOT}" && GOOS="${goos}" GOARCH="${goarch}" go build -o "${bin_dir}/ur${exe_suffix}" .); then|if false; then|g' \
    -e 's|(cd "${ROOT}" && go run "./cmd/ur-${app}" generate-skills|(cd "${ROOT}" \&\& go run . --app "${app}" generate-skills|g' \
    scripts/package-skill.sh \
    && UR_SWAGGER_DIR=/src/.swagger bash scripts/package-skill.sh /out/ur-package

FROM debian:bookworm-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates \
        iproute2 \
        iptables \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd -g 10001 runner \
    && useradd -m -u 10001 -g 10001 -s /usr/sbin/nologin runner

WORKDIR /app
COPY --from=builder /out/sandbox /app/sandbox
COPY --from=builder /out/ur /usr/local/bin/ur
COPY --from=builder /out/qjs /usr/local/bin/qjs
COPY --from=builder /src/sandbox/bin/ur-api /usr/local/bin/ur-api
COPY --from=builder /src/sandbox/bin/sandbox-skill /usr/local/bin/sandbox-skill
COPY --from=builder /out/ur-package/x64-linux/skill /opt/skills-store/common/ur-api
COPY --from=builder /out/ur-package/x64-linux/skill/ur-platform-manage /opt/skills-store/shared/ur-platform-manage
COPY --from=builder /out/ur-package/x64-linux/skill/ur-iot /opt/skills-store/shared/ur-iot
COPY --from=builder /out/ur-package/x64-linux/skill/ur-org-manage /opt/skills-store/shared/ur-org-manage
COPY --from=builder /out/ur-package/x64-linux/skill/ur-org-energy /opt/skills-store/shared/ur-org-energy
COPY --from=builder /out/ur-package/x64-linux/skill/ur-console /opt/skills-store/shared/ur-console
COPY backend/.swagger/core-api.json /opt/backend/.swagger/core-api.json
COPY backend/.swagger/things-api.json /opt/backend/.swagger/things-api.json

RUN for app in platform-manage iot org-manage org-energy console; do \
      printf '#!/usr/bin/env bash\nset -euo pipefail\nexec /usr/local/bin/ur --app %s "$@"\n' "$app" > "/usr/local/bin/ur-${app}" \
      && chmod +x "/usr/local/bin/ur-${app}"; \
    done

RUN chmod +x /usr/local/bin/ur /usr/local/bin/qjs /usr/local/bin/ur-api /usr/local/bin/sandbox-skill \
    && find /opt/skills-store/common/ur-api -type f -name '*.sh' -exec chmod +x {} + \
    && mkdir -p /home/runner/.ur && chown -R runner:runner /home/runner

ENV HOME=/home/runner

EXPOSE 8080

ENTRYPOINT ["/app/sandbox"]
