package helps

import (
	"context"
	"crypto/rand"
	"math/big"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// AntigravityJitterConfig configures stochastic network jitter and rate smoothing.
type AntigravityJitterConfig struct {
	Enabled             bool
	MinJitter           time.Duration
	MaxJitter           time.Duration
	MinInterRequestGap  time.Duration
	MaxConcurrentPacing int
}

// DefaultAntigravityJitterConfig provides production-calibrated defaults matching human/Electron pacing.
func DefaultAntigravityJitterConfig() AntigravityJitterConfig {
	return AntigravityJitterConfig{
		Enabled:             true,
		MinJitter:           15 * time.Millisecond,
		MaxJitter:           85 * time.Millisecond,
		MinInterRequestGap:  50 * time.Millisecond,
		MaxConcurrentPacing: 1024,
	}
}

// LatencyPercentiles holds statistical latency measurements.
type LatencyPercentiles struct {
	Count int
	Min   time.Duration
	Max   time.Duration
	Avg   time.Duration
	P50   time.Duration
	P90   time.Duration
	P99   time.Duration
}

// AntigravityRateSmoother applies stochastic micro-jitter and rate smoothing to eliminate bot-like burst signatures.
type AntigravityRateSmoother struct {
	cfg         AntigravityJitterConfig
	mu          sync.Mutex
	lastRequest map[string]time.Time

	// Empirical telemetry
	totalSmoothed atomic.Int64
	delays        []time.Duration
	delaysMu      sync.Mutex
}

var (
	defaultAntigravityRateSmoother *AntigravityRateSmoother
	rateSmootherInitOnce           sync.Once
)

// GetDefaultAntigravityRateSmoother returns the singleton rate smoother.
func GetDefaultAntigravityRateSmoother() *AntigravityRateSmoother {
	rateSmootherInitOnce.Do(func() {
		defaultAntigravityRateSmoother = NewAntigravityRateSmoother(DefaultAntigravityJitterConfig())
	})
	return defaultAntigravityRateSmoother
}

// NewAntigravityRateSmoother constructs an instance of AntigravityRateSmoother.
func NewAntigravityRateSmoother(cfg AntigravityJitterConfig) *AntigravityRateSmoother {
	if cfg.MinJitter <= 0 {
		cfg.MinJitter = 10 * time.Millisecond
	}
	if cfg.MaxJitter <= cfg.MinJitter {
		cfg.MaxJitter = cfg.MinJitter + 50*time.Millisecond
	}
	if cfg.MinInterRequestGap <= 0 {
		cfg.MinInterRequestGap = 30 * time.Millisecond
	}
	if cfg.MaxConcurrentPacing <= 0 {
		cfg.MaxConcurrentPacing = 1024
	}

	return &AntigravityRateSmoother{
		cfg:         cfg,
		lastRequest: make(map[string]time.Time),
		delays:      make([]time.Duration, 0, 1024),
	}
}

// Smooth paces an outgoing request for sessionID by applying stochastic jitter and rate smoothing.
func (s *AntigravityRateSmoother) Smooth(ctx context.Context, sessionID string) time.Duration {
	if !s.cfg.Enabled {
		return 0
	}

	var requiredDelay time.Duration
	now := time.Now()

	s.mu.Lock()
	if last, exists := s.lastRequest[sessionID]; exists {
		elapsed := now.Sub(last)
		if elapsed < s.cfg.MinInterRequestGap {
			requiredDelay = s.cfg.MinInterRequestGap - elapsed
		}
	}
	// Manage map size
	if len(s.lastRequest) >= s.cfg.MaxConcurrentPacing {
		// Clear stale entries
		for k, t := range s.lastRequest {
			if now.Sub(t) > 5*time.Minute {
				delete(s.lastRequest, k)
			}
		}
	}
	s.lastRequest[sessionID] = now
	s.mu.Unlock()

	// Compute stochastic micro-jitter in range [MinJitter, MaxJitter]
	jitter := s.ComputeStochasticJitter()
	totalDelay := requiredDelay + jitter

	if totalDelay > 0 {
		select {
		case <-ctx.Done():
			return 0
		case <-time.After(totalDelay):
		}
	}

	s.totalSmoothed.Add(1)
	s.recordDelay(totalDelay)
	return totalDelay
}

// ComputeStochasticJitter generates a crypto-secure pseudorandom duration in [MinJitter, MaxJitter].
func (s *AntigravityRateSmoother) ComputeStochasticJitter() time.Duration {
	diff := s.cfg.MaxJitter - s.cfg.MinJitter
	if diff <= 0 {
		return s.cfg.MinJitter
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(diff)))
	if err != nil {
		return s.cfg.MinJitter + diff/2
	}
	return s.cfg.MinJitter + time.Duration(n.Int64())
}

func (s *AntigravityRateSmoother) recordDelay(d time.Duration) {
	s.delaysMu.Lock()
	defer s.delaysMu.Unlock()
	if len(s.delays) < 10000 {
		s.delays = append(s.delays, d)
	}
}

// TotalSmoothed returns total smoothed requests.
func (s *AntigravityRateSmoother) TotalSmoothed() int64 {
	return s.totalSmoothed.Load()
}

// CalculatePercentiles evaluates empirical statistical percentiles for delays.
func (s *AntigravityRateSmoother) CalculatePercentiles() LatencyPercentiles {
	s.delaysMu.Lock()
	defer s.delaysMu.Unlock()

	n := len(s.delays)
	if n == 0 {
		return LatencyPercentiles{}
	}

	sorted := make([]time.Duration, n)
	copy(sorted, s.delays)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	var sum time.Duration
	for _, d := range sorted {
		sum += d
	}

	p50Idx := int(float64(n) * 0.50)
	p90Idx := int(float64(n) * 0.90)
	p99Idx := int(float64(n) * 0.99)
	if p50Idx >= n {
		p50Idx = n - 1
	}
	if p90Idx >= n {
		p90Idx = n - 1
	}
	if p99Idx >= n {
		p99Idx = n - 1
	}

	return LatencyPercentiles{
		Count: n,
		Min:   sorted[0],
		Max:   sorted[n-1],
		Avg:   sum / time.Duration(n),
		P50:   sorted[p50Idx],
		P90:   sorted[p90Idx],
		P99:   sorted[p99Idx],
	}
}
