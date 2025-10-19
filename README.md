# Terra Classic Oracle-Go

A production-ready oracle feeder for Terra Classic validators. Single Go binary, high performance, 20+ price sources.

**Key Features**: Price aggregation • Secure voting • Low resource usage (<100MB) • Fast startup (<1s)

---

## ⚡ Quick Start (5 minutes)

### 1. Build

```bash
git clone https://github.com/StrathCole/oracle-go.git
cd oracle-go
make build
```

### 2. Configure

```bash
cp config/config.yaml config/my-config.yaml
# Edit: validators, mnemonic_env, grpc_endpoints
```

### 3. Run

```bash
export ORACLE_MNEMONIC="your 24-word mnemonic"
./oracle-go --config config/my-config.yaml
```

### 4. Verify

```bash
curl http://localhost:8080/health       # Price server status
curl http://localhost:9091/metrics | grep oracle_  # Metrics
```

---

## 🔧 Configuration

### Minimal Setup

```yaml
mode: both  # "server", "feeder", or "both"

feeder:
  chain_id: columbus-5
  validators:
    - terravaloper1xxx...  # Your validator address
  mnemonic_env: ORACLE_MNEMONIC

  grpc_endpoints:
    - host: terra-classic-grpc.publicnode.com
      port: 443
      tls: true
    
  rpc_endpoints:
    - host: terra-classic-rpc.publicnode.com
      port: 443
      tls: true

sources:
  - type: cex
    name: binance
    enabled: true
    config:
      pairs:
        LUNC/USDT: LUNCUSDT
```

**See [config/config.yaml](config/config.yaml) for complete reference with all options.**

<details>
<summary><b>Runtime Modes</b> (click to expand)</summary>

| Mode | Description | Use Case |
|------|-------------|----------|
| `both` (default) | Price server + Feeder | Single-server deployment |
| `server` | Price server only | Shared price feed for multiple validators |
| `feeder` | Feeder only | Connect to external price server |

```bash
# Price server only
./oracle-go --config config.yaml --server

# Feeder only
./oracle-go --config config.yaml --feeder
```

</details>

---

## 🏭 Production Deployment

### Systemd Service

```ini
[Unit]
Description=Terra Classic Oracle
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=oracle
Group=oracle
WorkingDirectory=/opt/oracle-go
ExecStart=/opt/oracle-go/oracle-go --config /opt/oracle-go/config/config.yaml
Restart=always
RestartSec=10
Environment="ORACLE_MNEMONIC=<your_mnemonic_here>"

[Install]
WantedBy=multi-user.target
```

**Enable and start:**

```bash
sudo systemctl daemon-reload
sudo systemctl enable oracle-go
sudo systemctl start oracle-go
sudo journalctl -u oracle-go -f
```

### Docker

Build the image:

```bash
docker build -t oracle-go:latest .
```

Run the container:

```bash
docker run -d \
  --name oracle-go \
  -e ORACLE_FEEDER_MNEMONIC="your 24-word mnemonic" \
  -p 8080:8080 \
  -p 8081:8081 \
  -p 9091:9091 \
  -v $(pwd)/config/config.yaml:/oracle-go/config/config.yaml \
  oracle-go:latest
```

View logs:

```bash
docker logs -f oracle-go
```

Stop container:

```bash
docker stop oracle-go
```

---

## 📊 Monitoring & Metrics

### Health Checks

```bash
curl http://localhost:8080/health              # {"status":"ok"}
curl http://localhost:9091/metrics | grep oracle_
```

### Key Metrics

**Price Server:**

- `oracle_vote_submissions_total{status="success|failure"}` - Vote count
- `oracle_price_staleness_seconds{source="binance"}` - Price freshness
- `oracle_source_health{source="binance"}` - UP/DOWN (1/0)

**Feeder:**

- `oracle_vote_errors_total` - Voting errors
- `oracle_lcd_failovers_total` - LCD endpoint failovers

### Alerting

```yaml
- alert: OracleVoteFailure
  expr: rate(oracle_vote_submissions_total{status="failure"}[5m]) > 0.1
  annotations:
    summary: "Vote failure rate > 10%"

- alert: SourceDown
  expr: oracle_source_health == 0
  annotations:
    summary: "Source {{ $labels.source }} is down"

- alert: PriceStale
  expr: oracle_price_staleness_seconds > 300
  annotations:
    summary: "Price {{ $labels.symbol }} stale (>5min)"
```

---

## 💰 Price Sources (20+)

**Centralized Exchanges (CEX)**: Binance, CoinGecko, Kraken, Kucoin, Huobi, Bitfinex, Bybit, Gate.io, OKX, MEXC, CoinMarketCap

**Decentralized (DEX)**: Terraswap, Terraport, Garuda, PancakeSwap

**Oracle Aggregators**: Band Protocol

**Fiat**: ExchangeRate-API, Fixer, Frankfurter, IMF

<details>
<summary><b>Source Details</b> (click to expand)</summary>

### CEX Sources

| Exchange | WebSocket | Notes |
|----------|-----------|-------|
| **Binance** | ✅ | Primary LUNC source |
| **CoinGecko** | ❌ | Free: 10-30 calls/min |
| **Kraken** | ❌ | Good for BTC/ETH |
| **Kucoin** | ❌ | LUNC trading pairs |
| **Huobi** | ❌ | Asia-focused |
| **Bitfinex** | ❌ | BTC/ETH only |
| **Bybit** | ❌ | Derivatives focus |
| **Gate.io** | ❌ | Wide altcoin range |
| **OKX** | ❌ | No LUNC pairs |
| **MEXC** | ✅ | Emerging altcoins |
| **CoinMarketCap** | ❌ | API key required |

### DEX Sources (CosmWasm)

| DEX | Symbol |
|-----|--------|
| **Terraswap** | LUNC/USDC |
| **Terraport** | LUNC/USDC |
| **Garuda** | LUNC/USDC |

### DEX Sources (EVM)

| DEX | Symbol |
|-----|--------|
| **PancakeSwap** | LUNC/USDT |

### Fiat Sources

| Source | Notes |
|--------| ------- |
| **ExchangeRate-API** | Free tier: 1500 requests/month |
| **Fixer** | API key required |
| **Frankfurter** | Free, no key |
| **IMF** | Free, no key, web-scraper |

### SDR (Special Drawing Rights)

Automatically calculated from IMF rates (USD, EUR, CNY, JPY, GBP).

### Oracle aggregators

| Aggregator | Notes |
|------------|-------|
| **Band Protocol** | Decentralized oracle |

</details>

---

## 🔧 Troubleshooting

### RPC/gRPC Connection Failed

```bash
# Check endpoint
curl https://terra-classic-lcd.publicnode.com/cosmos/base/tendermint/v1beta1/node_info
# Add multiple fallback endpoints
```

### Invalid Mnemonic

- Verify 12 or 24 words
- Check `coin_type: 330` (Terra Classic)
- Ensure env var set: `echo $ORACLE_MNEMONIC`

### No Whitelisted Prices

```bash
terrad query oracle whitelist
# Verify sources provide denoms in whitelist
```

### Price Source Down

- Check source-specific logs: `journalctl -u oracle-go | grep source`
- Verify API keys if required
- Test endpoint manually

### Debug Mode

```yaml
logging:
  level: debug
```

```bash
journalctl -u oracle-go -f                # View all logs
journalctl -u oracle-go | grep "vote"     # Vote-only logs
journalctl -u oracle-go | grep "source"   # Source errors
```

### Dry-Run Testing

```yaml
feeder:
  dry_run: true
  verify: true  # Compare with on-chain rates
```

or

```bash
./build/oracle-go --feeder --dry-run
```

This will:

- ✅ Connect to RPC/gRPC
- ✅ Fetch prices
- ✅ Generate vote messages
- ✅ Verify against on-chain rates
- ❌ NOT submit transactions

---

## 👨‍💻 Development

### Building

```bash
git clone https://github.com/StrathCole/oracle-go.git
cd oracle-go
go mod tidy
go build -o oracle-go ./cmd/oracle-go
go test ./...              # Run tests
go run -race ./cmd/oracle-go  # Race detection
```

### Adding a New Price Source

1. **Create source:**

```go
package cex

type NewSource struct {
    *sources.BaseSource
}

func NewNewSource(config map[string]interface{}) (*NewSource, error) {
    base := sources.NewBaseSource("newsource", sources.SourceTypeCEX, config)
    return &NewSource{BaseSource: base}, nil
}

func (s *NewSource) Start(ctx context.Context) error {
    // Fetch prices and call s.SetPrice(symbol, price, time.Now())
    return nil
}

func (s *NewSource) Stop() error {
    // Cleanup
    return nil
}
```

2. **Register:**

Add to `registration.go` in `init()` function:

```go
    sources.Register("cex.newsource", func(cfg map[string]interface{}) (sources.Source, error) {
        return NewNewSource(cfg)
    })
```

3. **Add to config:**

```yaml
sources:
  - type: cex
    name: newsource
    enabled: true
    config:
      pairs:
        LUNC/USD: lunc-usd
```

### Testing & Linting

```bash
# Run tests
go test ./...
go test -cover ./...
go test ./pkg/server/sources/...

# Linting
make lint              # golangci-lint (25+ linters)
make fmt               # gofumpt formatting
make ci                # All checks (format, lint, test)

# Pre-commit
make fmt && make lint && make test
```

---

## �� Additional Documentation

- **[oracle-rework.md](oracle-rework.md)** - Implementation plan (13 phases, 18 weeks)
- **[.github/copilot-instructions.md](.github/copilot-instructions.md)** - Architecture & development guide
- **[config/config.yaml](config/config.yaml)** - Complete configuration reference
- **[EVENTSTREAM_IMPLEMENTATION.md](EVENTSTREAM_IMPLEMENTATION.md)** - Event-driven architecture

---

## 🤝 Contributing

1. Fork the repository
2. Create feature branch: `git checkout -b feature/amazing-feature`
3. Commit: `git commit -m 'Add feature'`
4. Push: `git push origin feature/amazing-feature`
5. Open Pull Request

See [oracle-rework.md](oracle-rework.md) for planned features.

---

## 📄 License

This project is licensed under the Apache License 2.0 - see the [LICENSE](LICENSE) file for details.

---

**Questions?** Check [.github/copilot-instructions.md](.github/copilot-instructions.md) for detailed
architecture and development guidance.
