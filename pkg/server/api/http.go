// Package api provides HTTP and WebSocket API endpoints for the price server.
package api

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/StrathCole/oracle-go/pkg/config"
	"github.com/StrathCole/oracle-go/pkg/logging"
	"github.com/StrathCole/oracle-go/pkg/metrics"
	"github.com/StrathCole/oracle-go/pkg/server/aggregator"
	"github.com/StrathCole/oracle-go/pkg/server/sources"
)

// Server represents the HTTP API server.
type Server struct {
	addr          string
	sources       []sources.Source
	sourceWeights map[string]float64 // Weight for each source in aggregation
	aggregator    aggregator.Aggregator
	server        *http.Server
	logger        *logging.Logger
	cacheTTL      time.Duration
	lastCache     map[string]sources.Price
	cacheTime     time.Time
	wsServer      *WebSocketServer // Optional WebSocket server for streaming
	tlsConfig     config.TLSConfig // TLS configuration
}

// NewServer creates a new HTTP API server.
func NewServer(addr string, sourcesSlice []sources.Source, agg aggregator.Aggregator, weights map[string]float64, cacheTTL time.Duration, tlsCfg config.TLSConfig, logger *logging.Logger) *Server {
	return &Server{
		addr:          addr,
		sources:       sourcesSlice,
		sourceWeights: weights,
		aggregator:    agg,
		logger:        logger,
		cacheTTL:      cacheTTL,
		lastCache:     make(map[string]sources.Price),
		tlsConfig:     tlsCfg,
	}
}

// SetWebSocketServer sets the WebSocket server for streaming updates.
func (s *Server) SetWebSocketServer(ws *WebSocketServer) {
	s.wsServer = ws
}

// Start starts the HTTP server.
func (s *Server) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/v1/prices", s.handlePrices)
	mux.HandleFunc("/latest", s.handlePrices) // Compatibility with TypeScript feeder

	s.server = &http.Server{
		Addr:              s.addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	// Configure TLS if enabled
	if s.tlsConfig.Enabled {
		if s.tlsConfig.Cert == "" || s.tlsConfig.Key == "" {
			return fmt.Errorf("TLS enabled but cert/key not provided")
		}

		// Configure TLS with secure defaults
		s.server.TLSConfig = &tls.Config{
			MinVersion: tls.VersionTLS12,
			CipherSuites: []uint16{
				tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
				tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
				tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
				tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			},
		}

		s.logger.Info("Starting HTTPS server", "addr", s.addr)
		if err := s.server.ListenAndServeTLS(s.tlsConfig.Cert, s.tlsConfig.Key); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("HTTPS server error: %w", err)
		}
	} else {
		s.logger.Info("Starting HTTP server", "addr", s.addr)
		if err := s.server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("HTTP server error: %w", err)
		}
	}
	return nil
}

// Stop gracefully stops the HTTP server.
func (s *Server) Stop(ctx context.Context) error {
	if s.server != nil {
		s.logger.Info("Stopping HTTP server")
		return s.server.Shutdown(ctx)
	}
	return nil
}

// handleHealth handles /health endpoint.
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	start := time.Now()
	defer func() {
		metrics.RecordHTTPRequest("/health", "200", time.Since(start))
	}()

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("OK"))
}

// handlePrices handles /v1/prices and /latest endpoints.
func (s *Server) handlePrices(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	status := "200"
	defer func() {
		metrics.RecordHTTPRequest(r.URL.Path, status, time.Since(start))
	}()

	// Check cache
	if time.Since(s.cacheTime) < s.cacheTTL && len(s.lastCache) > 0 {
		s.sendJSON(w, s.convertToArray(s.lastCache))
		return
	}

	// Gather prices from all sources
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	sourcePrices := make(map[string]map[string]sources.Price)
	for _, source := range s.sources {
		if !source.IsHealthy() {
			s.logger.Warn("Skipping unhealthy source", "source", source.Name())
			continue
		}

		prices, err := source.GetPrices(ctx)
		if err != nil {
			s.logger.Error("Failed to get prices from source", "source", source.Name(), "error", err.Error())
			continue
		}

		sourcePrices[source.Name()] = prices
	}

	if len(sourcePrices) == 0 {
		status = "503"
		http.Error(w, "No prices available", http.StatusServiceUnavailable)
		return
	}

	// Aggregate prices using configured aggregator with source weights
	aggregatedPrices, err := s.aggregator.Aggregate(sourcePrices, s.sourceWeights)
	if err != nil {
		status = "503"
		s.logger.Error("Failed to aggregate prices", "error", err)
		http.Error(w, "Failed to aggregate prices", http.StatusServiceUnavailable)
		return
	}

	// Update cache
	s.lastCache = aggregatedPrices
	s.cacheTime = time.Now()

	// Send to WebSocket clients if enabled
	if s.wsServer != nil {
		s.wsServer.SendUpdate(aggregatedPrices)
	}

	s.sendJSON(w, s.convertToArray(aggregatedPrices))
}

// convertToArray converts price map to array format.
func (s *Server) convertToArray(prices map[string]sources.Price) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(prices))
	for _, price := range prices {
		result = append(result, map[string]interface{}{
			"symbol": price.Symbol,
			"price":  price.Price.String(),
		})
	}
	return result
}

// sendJSON sends a JSON response.
func (s *Server) sendJSON(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(data); err != nil {
		s.logger.Error("Failed to encode JSON response", "error", err)
	}
}
