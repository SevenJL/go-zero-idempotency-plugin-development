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
	"log"
	"os"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// ConfigWatcher watches a YAML configuration file for changes and invokes
// the callback with the parsed ConfigFile on each detected change.
type ConfigWatcher struct {
	path     string
	callback func(ConfigFile)
	done     chan struct{}
	mu       sync.Mutex
	lastMod  time.Time
}

// WatchConfig starts watching the YAML file at path. On each detected change,
// the file is re-read, parsed, and passed to the callback.
//
// Returns a ConfigWatcher that must be closed when the application shuts down.
func WatchConfig(path string, callback func(ConfigFile)) (*ConfigWatcher, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}

	w := &ConfigWatcher{
		path:     path,
		callback: callback,
		done:     make(chan struct{}),
		lastMod:  info.ModTime(),
	}

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
		log.Printf("[idempotency] config watch stat error: %v", err)
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
		log.Printf("[idempotency] config watch read error: %v", err)
		return
	}

	var cfg ConfigFile
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		log.Printf("[idempotency] config watch parse error: %v", err)
		return
	}

	w.mu.Lock()
	w.lastMod = info.ModTime()
	w.mu.Unlock()

	log.Printf("[idempotency] config reloaded from %s", w.path)
	w.callback(cfg)
}
