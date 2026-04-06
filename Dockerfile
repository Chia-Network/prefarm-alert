FROM golang:1 AS builder

COPY . /app
WORKDIR /app
RUN make build

FROM chianetwork/chia-docker:2.5.4

# Add internal-custody to the venv provided by chia-docker
RUN . /chia-blockchain/venv/bin/activate && \
    pip install --extra-index-url https://pypi.chia.net/simple/ chia-internal-custody

COPY --from=builder /app/bin/prefarm-alert /prefarm-alert

CMD ["/prefarm-alert"]
