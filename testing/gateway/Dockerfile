# DStarGateway (F4FXL's wxWidgets-free port of G4KLX ircDDBGateway) with one
# homebrew repeater module, for testing RefConnect's repeater links offline.
# See README.md.
FROM debian:bookworm-slim AS build
RUN apt-get update && apt-get install -y --no-install-recommends \
      build-essential ca-certificates git libboost-dev libcurl4-openssl-dev \
    && rm -rf /var/lib/apt/lists/*
ARG DSTARGATEWAY_REF=7b75de605dca542a2953dd74c16f0eac96a8944e
RUN git clone https://github.com/F4FXL/DStarGateway.git /src \
    && git -C /src checkout "$DSTARGATEWAY_REF" \
    && make -C /src -j"$(nproc)"

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends libcurl4 \
    && rm -rf /var/lib/apt/lists/*
COPY --from=build /src/DStarGateway/dstargateway /usr/local/bin/
COPY --from=build /src/Data /usr/local/share/dstargateway.d
COPY dstargateway.cfg /etc/dstargateway.cfg.in
COPY entrypoint.sh /usr/local/bin/
# No reflector host lists: the gateway resolves every entry at startup.
RUN rm /usr/local/share/dstargateway.d/*_Hosts.txt \
    && mkdir -p /var/log/dstargateway /usr/local/share/dstargateway.d/hostfiles.d
# DPlus, DExtra, DCS incoming links.
EXPOSE 20001/udp 30001/udp 30051/udp
CMD ["entrypoint.sh"]
