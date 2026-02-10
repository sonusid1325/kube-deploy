package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"
)

var (
	version   = getEnv("VERSION", "v1")
	port      = getEnv("PORT", "8080")
	failAfter = getEnv("FAIL_AFTER", "")

	startTime    time.Time
	healthy      atomic.Bool
	ready        atomic.Bool
	requestCount atomic.Int64
)

func main() {
	startTime = time.Now()
	healthy.Store(true)
	ready.Store(true)

	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)
	log.Printf("[goserver %s] starting on port %s (pid=%d)", version, port, os.Getpid())

	// If FAIL_AFTER is set, schedule a failure after the given duration.
	// This is used for testing automated rollback scenarios.
	if failAfter != "" {
		dur, err := time.ParseDuration(failAfter)
		if err != nil {
			log.Printf("[goserver] WARNING: invalid FAIL_AFTER duration %q: %v", failAfter, err)
		} else {
			log.Printf("[goserver] will simulate failure after %v", dur)
			go func() {
				time.Sleep(dur)
				log.Printf("[goserver] FAIL_AFTER triggered — marking as unhealthy and not ready")
				healthy.Store(false)
				ready.Store(false)

				// If FAIL_MODE is "crash", exit the process to trigger CrashLoopBackOff.
				if getEnv("FAIL_MODE", "unhealthy") == "crash" {
					log.Printf("[goserver] FAIL_MODE=crash — exiting with code 1")
					os.Exit(1)
				}
			}()
		}
	}

	mux := http.NewServeMux()

	// Health endpoint — used by Kubernetes liveness probe and kube-deploy health monitor.
	mux.HandleFunc("/healthz", handleHealthz)

	// Readiness endpoint — used by Kubernetes readiness probe.
	mux.HandleFunc("/readyz", handleReadyz)

	// Root endpoint — returns version info and basic stats.
	mux.HandleFunc("/", handleRoot)

	// Info endpoint — returns detailed server information as JSON.
	mux.HandleFunc("/info", handleInfo)

	// Admin endpoints for testing — allows toggling health/readiness at runtime.
	mux.HandleFunc("/admin/healthy", handleSetHealthy)
	mux.HandleFunc("/admin/unhealthy", handleSetUnhealthy)
	mux.HandleFunc("/admin/ready", handleSetReady)
	mux.HandleFunc("/admin/not-ready", handleSetNotReady)

	server := &http.Server{
		Addr:         ":" + port,
		Handler:      loggingMiddleware(mux),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Graceful shutdown on SIGINT/SIGTERM.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		log.Printf("[goserver] received shutdown signal, draining connections...")

		// Mark as not ready immediately so the readiness probe fails
		// and Kubernetes stops sending new traffic.
		ready.Store(false)

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("[goserver] shutdown error: %v", err)
		}
	}()

	log.Printf("[goserver %s] listening on :%s", version, port)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("[goserver] fatal: %v", err)
	}

	log.Printf("[goserver %s] shut down cleanly", version)
}

// ============================================================================
// Handlers
// ============================================================================

func handleHealthz(w http.ResponseWriter, r *http.Request) {
	if !healthy.Load() {
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprintf(w, "unhealthy\n")
		return
	}
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "ok\n")
}

func handleReadyz(w http.ResponseWriter, r *http.Request) {
	if !ready.Load() {
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprintf(w, "not ready\n")
		return
	}
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "ready\n")
}

func handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	requestCount.Add(1)

	w.Header().Set("Content-Type", "text/plain")
	fmt.Fprintf(w, "goserver %s\n", version)
	fmt.Fprintf(w, "uptime: %s\n", time.Since(startTime).Round(time.Second))
	fmt.Fprintf(w, "requests: %d\n", requestCount.Load())
	fmt.Fprintf(w, "healthy: %v\n", healthy.Load())
	fmt.Fprintf(w, "ready: %v\n", ready.Load())
	fmt.Fprintf(w, "hostname: %s\n", hostname())
}

func handleInfo(w http.ResponseWriter, r *http.Request) {
	requestCount.Add(1)

	info := map[string]interface{}{
		"version":    version,
		"hostname":   hostname(),
		"uptime_s":   time.Since(startTime).Seconds(),
		"healthy":    healthy.Load(),
		"ready":      ready.Load(),
		"requests":   requestCount.Load(),
		"pid":        os.Getpid(),
		"port":       port,
		"started":    startTime.UTC().Format(time.RFC3339),
		"fail_after": failAfter,
		"env": map[string]string{
			"VERSION":    version,
			"PORT":       port,
			"FAIL_AFTER": failAfter,
			"FAIL_MODE":  getEnv("FAIL_MODE", "unhealthy"),
		},
	}

	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(info); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func handleSetHealthy(w http.ResponseWriter, r *http.Request) {
	healthy.Store(true)
	log.Printf("[goserver] admin: marked as healthy")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "marked healthy\n")
}

func handleSetUnhealthy(w http.ResponseWriter, r *http.Request) {
	healthy.Store(false)
	log.Printf("[goserver] admin: marked as unhealthy")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "marked unhealthy\n")
}

func handleSetReady(w http.ResponseWriter, r *http.Request) {
	ready.Store(true)
	log.Printf("[goserver] admin: marked as ready")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "marked ready\n")
}

func handleSetNotReady(w http.ResponseWriter, r *http.Request) {
	ready.Store(false)
	log.Printf("[goserver] admin: marked as not ready")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "marked not ready\n")
}

// ============================================================================
// Middleware
// ============================================================================

// loggingMiddleware logs each incoming HTTP request with method, path, status,
// and response time. Health/readiness probes are logged at a reduced rate to
// avoid excessive log spam from kubelet.
func loggingMiddleware(next http.Handler) http.Handler {
	var probeLogCounter atomic.Int64

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

		next.ServeHTTP(rw, r)

		duration := time.Since(start)

		// Log probe endpoints every 10th request to reduce noise.
		if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
			count := probeLogCounter.Add(1)
			if count%10 != 1 {
				return
			}
		}

		log.Printf("[goserver] %s %s %d %v %s",
			r.Method,
			r.URL.Path,
			rw.statusCode,
			duration.Round(time.Microsecond),
			r.UserAgent(),
		)
	})
}

// responseWriter wraps http.ResponseWriter to capture the status code.
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// ============================================================================
// Helpers
// ============================================================================

func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return fallback
}

func hostname() string {
	h, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return h
}
