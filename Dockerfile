FROM golang:1 as builder

COPY . /app
WORKDIR /app
RUN make build

FROM chianetwork/chia-docker:latest

# Add internal-custody to the venv provided by chia-docker
RUN . /chia-blockchain/venv/bin/activate && \
    pip install --extra-index-url https://pypi.chia.net/simple/ chia-internal-custody

COPY docker-start.sh /usr/local/bin/
RUN chmod +x /usr/local/bin/docker-start.sh
COPY --from=builder /app/bin/prefarm-alert /prefarm-alert
