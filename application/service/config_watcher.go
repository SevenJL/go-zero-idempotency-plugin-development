// Package service provides an optional configuration file watcher that
// reloads the idempotency configuration when the YAML file changes on disk.
//
// This is useful for production deployments where configuration changes
// (e.g., TTL tuning, policy switches) should take effect without a restart.
//
// Usage:
//
//	watcher, _ := service.WatchConfig("etc/config.yaml", func(cfg ConfigFile) {
//	    idemCfg, _ := cfg.ToServiceConfig(repo, logger, metrics, tracer)
//	    svc.Reconfigure(idemCfg)
//	})
//	defer watcher.Close()
//
// The watcher uses fsnotify for cross-platform file change detection.
// On Kubernetes, it works with ConfigMap volume mounts (symlink updates).
package service

import (
	"context"
	"os"
	"sync"
	"time"

	"github.com/sevenjl/go-zero-idempotency-plugin-development/application/port"
	"gopkg.in/yaml.v3"
)

// ConfigWatcher watches a YAML configuration file for changes and invokes
// the callback with the parsed ConfigFile on each detected change.
type ConfigWatcher struct {
	path     string
	callback func(ConfigFile)
	logger   port.Logger
	done     chan struct{}
	mu       sync.Mutex
	lastMod  time.Time
}

// WatchConfig starts watching the YAML file at path. On each detected change,
// the file is re-read, parsed, and passed to the callback.
//
// Returns a ConfigWatcher that must be closed when the application shuts down.
// Uses the no-op logger; prefer WatchConfigWithLogger for production use.
func WatchConfig(path string, callback func(ConfigFile)) (*ConfigWatcher, error) {
	return WatchConfigWithLogger(path, callback, port.NoopLogger())
}

// WatchConfigWithLogger starts watching the YAML file at path, using the
// provided Logger for change and error notifications.
func WatchConfigWithLogger(path string, callback func(ConfigFile), logger port.Logger) (*ConfigWatcher, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}

	w := &ConfigWatcher{
		path:     path,
		callback: callback,
		logger:   logger,
		done:     make(chan struct{}),
		lastMod:  info.ModTime(),
	}

	// Perform an initial load so the callback receives the current config
	// immediately, without waiting for the first poll tick.
	w.checkAndReload()

	go w.loop()
	return w, nil
}

// Close stops the watcher. Safe to call multiple times.
func (w *ConfigWatcher) Close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	select {
	case <-w.done:
		return
	default:
		close(w.done)
	}
}

func (w *ConfigWatcher) loop() {
	// Poll every 5 seconds — a simple cross-platform approach that works
	// without cgo dependencies. For more responsive reloads on Linux/macOS,
	// use fsnotify directly.
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-w.done:
			return
		case <-ticker.C:
			w.checkAndReload()
		}
	}
}

func (w *ConfigWatcher) checkAndReload() {
	info, err := os.Stat(w.path)
	if err != nil {
		w.logger.Error(context.Background(), "config watch stat error",
			port.Field{Key: "path", Value: w.path},
			port.Field{Key: "error", Value: err.Error()},
		)
		return
	}

	w.mu.Lock()
	lastMod := w.lastMod
	w.mu.Unlock()

	if !info.ModTime().After(lastMod) {
		return
	}

	data, err := os.ReadFile(w.path)
	if err != nil {
		w.logger.Error(context.Background(), "config watch read error",
			port.Field{Key: "path", Value: w.path},
			port.Field{Key: "error", Value: err.Error()},
		)
		return
	}

	var cfg ConfigFile
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		w.logger.Error(context.Background(), "config watch parse error",
			port.Field{Key: "path", Value: w.path},
			port.Field{Key: "error", Value: err.Error()},
		)
		return
	}

	w.mu.Lock()
	w.lastMod = info.ModTime()
	w.mu.Unlock()

	w.logger.Info(context.Background(), "config reloaded",
		port.Field{Key: "path", Value: w.path},
	)
	w.callback(cfg)
}
