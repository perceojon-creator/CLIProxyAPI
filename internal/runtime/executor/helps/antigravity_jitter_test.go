package helps

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestAntigravityRateSmoother_JitterBoundsAndDistribution(t *testing.T) {
	cfg := AntigravityJitterConfig{
		Enabled:            true,
		MinJitter:          10 * time.Millisecond,
		MaxJitter:          30 * time.Millisecond,
		MinInterRequestGap: 20 * time.Millisecond,
	}

	smoother := NewAntigravityRateSmoother(cfg)

	// Collect 100 jitter samples
	for i := 0; i < 100; i++ {
		j := smoother.ComputeStochasticJitter()
		if j < cfg.MinJitter || j > cfg.MaxJitter {
			t.Fatalf("Jitter %v out of range [%v, %v]", j, cfg.MinJitter, cfg.MaxJitter)
		}
	}
	t.Log(" [PASS] 100 muestras de jitter estocástico verificadas dentro del rango [10ms, 30ms]")
}

func TestAntigravityRateSmoother_SmoothInterRequestPacing(t *testing.T) {
	cfg := AntigravityJitterConfig{
		Enabled:            true,
		MinJitter:          2 * time.Millisecond,
		MaxJitter:          5 * time.Millisecond,
		MinInterRequestGap: 25 * time.Millisecond,
	}

	smoother := NewAntigravityRateSmoother(cfg)
	ctx := context.Background()
	session := "session-pace-test"

	start := time.Now()
	_ = smoother.Smooth(ctx, session)
	// Immediate second call should be paced by MinInterRequestGap + jitter
	d2 := smoother.Smooth(ctx, session)
	totalElapsed := time.Since(start)

	if d2 < 20*time.Millisecond {
		t.Fatalf("Second call delay = %v, expected at least ~20ms pacing", d2)
	}
	if totalElapsed < 22*time.Millisecond {
		t.Fatalf("Total elapsed %v, expected >= 22ms", totalElapsed)
	}

	t.Logf(" [PASS] Pacing inter-request aplicado: delay=%v, elapsed=%v", d2, totalElapsed)
}

func TestAntigravityRateSmoother_ContextCancellation(t *testing.T) {
	cfg := AntigravityJitterConfig{
		Enabled:            true,
		MinJitter:          200 * time.Millisecond,
		MaxJitter:          300 * time.Millisecond,
		MinInterRequestGap: 200 * time.Millisecond,
	}

	smoother := NewAntigravityRateSmoother(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	start := time.Now()
	delay := smoother.Smooth(ctx, "session-cancel-test")
	elapsed := time.Since(start)

	if elapsed > 100*time.Millisecond {
		t.Fatalf("Smooth blocked for %v despite canceled context", elapsed)
	}
	if delay != 0 {
		t.Fatalf("Expected delay 0 on cancel, got %v", delay)
	}
	t.Log(" [PASS] Cancelacion de contexto respetada de forma no bloqueante")
}

func TestAntigravityRateSmoother_ConcurrentBenchmarkAndStatisticalPercentiles(t *testing.T) {
	cfg := AntigravityJitterConfig{
		Enabled:            true,
		MinJitter:          1 * time.Millisecond,
		MaxJitter:          5 * time.Millisecond,
		MinInterRequestGap: 2 * time.Millisecond,
	}

	smoother := NewAntigravityRateSmoother(cfg)
	const concurrentWorkers = 20
	const iterationsPerWorker = 25
	var wg sync.WaitGroup

	start := time.Now()
	for w := 0; w < concurrentWorkers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for i := 0; i < iterationsPerWorker; i++ {
				smoother.Smooth(context.Background(), "session-worker")
			}
		}(w)
	}
	wg.Wait()
	duration := time.Since(start)

	totalOps := concurrentWorkers * iterationsPerWorker
	opsPerSec := float64(totalOps) / duration.Seconds()
	stats := smoother.CalculatePercentiles()

	t.Log("=== FASE 5: MÉTRICAS ESTADÍSTICAS EMPÍRICAS DE JITTER Y SUAVIZADO ===")
	t.Logf(" Total Operaciones:    %d", totalOps)
	t.Logf(" Tiempo Total:         %v", duration)
	t.Logf(" Rendimiento:          %.2f ops/sec", opsPerSec)
	t.Logf(" Latencia Mínima:      %v", stats.Min)
	t.Logf(" Latencia Media (Avg): %v", stats.Avg)
	t.Logf(" Latencia P50:         %v", stats.P50)
	t.Logf(" Latencia P90:         %v", stats.P90)
	t.Logf(" Latencia P99:         %v", stats.P99)
	t.Logf(" Latencia Máxima:      %v", stats.Max)

	if stats.Count != totalOps {
		t.Fatalf("Stats count = %d, want %d", stats.Count, totalOps)
	}
	if stats.Min > stats.Avg || stats.Avg > stats.P99 {
		t.Fatalf("Inconsistent percentiles: Min=%v, Avg=%v, P99=%v", stats.Min, stats.Avg, stats.P99)
	}
}
