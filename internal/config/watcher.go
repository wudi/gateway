package config

import (
	"path/filepath"
	"sync"
	"time"

	"github.com/example/gateway/internal/logging"
	"github.com/fsnotify/fsnotify"
	"go.uber.org/zap"
)

// Watcher watches configuration files for changes
type Watcher struct {
	watcher    *fsnotify.Watcher
	loader     *Loader
	configPath string
	callbacks  []func(*Config)
	mu         sync.RWMutex
	debounce   time.Duration
	lastConfig *Config
}

// NewWatcher creates a new configuration watcher
func NewWatcher(configPath string) (*Watcher, error) {
	fsWatcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	w := &Watcher{
		watcher:    fsWatcher,
		loader:     NewLoader(),
		configPath: configPath,
		callbacks:  make([]func(*Config), 0),
		debounce:   500 * time.Millisecond,
	}

	// Load initial config
	cfg, err := w.loader.Load(configPath)
	if err != nil {
		fsWatcher.Close()
		return nil, err
	}
	w.lastConfig = cfg

	return w, nil
}

// OnChange registers a callback for config changes
func (w *Watcher) OnChange(callback func(*Config)) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.callbacks = append(w.callbacks, callback)
}

// Start begins watching for configuration changes
func (w *Watcher) Start() error {
	// Watch the directory containing the config file
	dir := filepath.Dir(w.configPath)
	if err := w.watcher.Add(dir); err != nil {
		return err
	}

	go w.watch()
	return nil
}

// watch monitors for file changes
func (w *Watcher) watch() {
	var debounceTimer *time.Timer
	var lastEvent time.Time

	for {
		select {
		case event, ok := <-w.watcher.Events:
			if !ok {
				return
			}

			// Only react to our config file
			if filepath.Base(event.Name) != filepath.Base(w.configPath) {
				continue
			}

			// Only react to write/create events
			if event.Op&(fsnotify.Write|fsnotify.Create) == 0 {
				continue
			}

			// Debounce rapid events
			now := time.Now()
			if now.Sub(lastEvent) < w.debounce {
				if debounceTimer != nil {
					debounceTimer.Stop()
				}
			}
			lastEvent = now

			debounceTimer = time.AfterFunc(w.debounce, func() {
				w.reload()
			})

		case err, ok := <-w.watcher.Errors:
			if !ok {
				return
			}
			logging.Error("config watcher error", zap.Error(err))
		}
	}
}

// reload loads the config and notifies callbacks
func (w *Watcher) reload() {
	cfg, err := w.loader.Load(w.configPath)
	if err != nil {
		logging.Error("failed to reload config", zap.Error(err))
		return
	}

	w.mu.Lock()
	w.lastConfig = cfg
	callbacks := make([]func(*Config), len(w.callbacks))
	copy(callbacks, w.callbacks)
	w.mu.Unlock()

	logging.Info("configuration reloaded", zap.String("path", w.configPath))

	// Notify all callbacks
	for _, cb := range callbacks {
		go cb(cfg)
	}
}

// GetConfig returns the current configuration
func (w *Watcher) GetConfig() *Config {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.lastConfig
}

// Stop stops watching for changes
func (w *Watcher) Stop() error {
	return w.watcher.Close()
}

// SetDebounce sets the debounce duration for file changes
func (w *Watcher) SetDebounce(d time.Duration) {
	w.debounce = d
}
