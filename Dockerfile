FROM golang:1.26.0 AS builder

ENV GOPROXY=https://goproxy.cn,https://proxy.golang.org,direct
ENV GOSUMDB=sum.golang.google.cn

WORKDIR /src
COPY .swagger ./.swagger
COPY share ./share
COPY claw ./claw
COPY cli/ur ./cli/ur
WORKDIR /src/claw
RUN go build -ldflags="-s -w" -o /out/claw .
WORKDIR /src/cli/ur
RUN go build -o /out/ur .
RUN for app in platform-manage iot org-manage org-energy console; do \
      go build -o "/out/ur-${app}" "./cmd/ur-${app}"; \
    done
RUN UR_SWAGGER_DIR=/src/.swagger bash scripts/package-skill.sh /out/ur-package

FROM node:24-bookworm-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates \
        bash \
        curl \
        iproute2 \
        iptables \
        util-linux \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd -g 10001 runner \
    && useradd -m -u 10001 -g 10001 -s /usr/sbin/nologin runner

WORKDIR /app
COPY --from=builder /out/claw /app/claw
COPY --from=builder /out/ur /usr/local/bin/ur
COPY --from=builder /out/ur-platform-manage /usr/local/bin/ur-platform-manage
COPY --from=builder /out/ur-iot /usr/local/bin/ur-iot
COPY --from=builder /out/ur-org-manage /usr/local/bin/ur-org-manage
COPY --from=builder /out/ur-org-energy /usr/local/bin/ur-org-energy
COPY --from=builder /out/ur-console /usr/local/bin/ur-console
COPY claw/bin/ur-api /usr/local/bin/ur-api
COPY claw/bin/claw-skill /usr/local/bin/claw-skill
COPY --from=builder /out/ur-package/skill /opt/skills-store/common/ur-api
COPY --from=builder /out/ur-package/skill/ur-platform-manage /opt/skills-store/shared/ur-platform-manage
COPY --from=builder /out/ur-package/skill/ur-iot /opt/skills-store/shared/ur-iot
COPY --from=builder /out/ur-package/skill/ur-org-manage /opt/skills-store/shared/ur-org-manage
COPY --from=builder /out/ur-package/skill/ur-org-energy /opt/skills-store/shared/ur-org-energy
COPY --from=builder /out/ur-package/skill/ur-console /opt/skills-store/shared/ur-console
COPY .swagger/core-api.json /opt/backend/.swagger/core-api.json
COPY .swagger/things-api.json /opt/backend/.swagger/things-api.json

RUN chmod +x /usr/local/bin/ur /usr/local/bin/ur-platform-manage /usr/local/bin/ur-iot \
        /usr/local/bin/ur-org-manage /usr/local/bin/ur-org-energy /usr/local/bin/ur-console \
        /usr/local/bin/ur-api /usr/local/bin/claw-skill \
    && find /opt/skills-store/common/ur-api -type f -name '*.sh' -exec chmod +x {} + \
    && mkdir -p /home/runner/.ur && chown -R runner:runner /home/runner

ENV HOME=/home/runner

EXPOSE 8080

ENTRYPOINT ["/app/claw"]
