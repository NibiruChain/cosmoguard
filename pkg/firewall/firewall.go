package firewall

import (
	"fmt"
	"path/filepath"
	"sync"

	"github.com/fsnotify/fsnotify"
	log "github.com/sirupsen/logrus"
)

type Firewall struct {
	cfgFile   string
	cfg       *Config
	lcdProxy  *HttpProxy
	grpcProxy *GrpcProxy
	mu        sync.Mutex
}

func New(path string) (*Firewall, error) {
	firewall := &Firewall{cfgFile: path}
	if err := firewall.loadConfig(); err != nil {
		return nil, err
	}

	lcdProxy, err := NewHttpProxy(
		fmt.Sprintf("%s:%d", firewall.cfg.Host, firewall.cfg.LcdPort),
		fmt.Sprintf("http://%s:%d", firewall.cfg.Node.Host, firewall.cfg.Node.LcdPort),
		firewall.cfg.Cache,
	)
	if err != nil {
		return nil, fmt.Errorf("error setting up lcd firewall proxy: %v", err)
	}
	firewall.lcdProxy = lcdProxy

	grpcProxy, err := NewGrpcProxy(
		fmt.Sprintf("%s:%d", firewall.cfg.Host, firewall.cfg.GrpcPort),
		fmt.Sprintf("%s:%d", firewall.cfg.Node.Host, firewall.cfg.Node.GrpcPort),
	)
	if err != nil {
		return nil, fmt.Errorf("error setting up grpc firewall proxy: %v", err)
	}
	firewall.grpcProxy = grpcProxy

	return firewall, nil
}

func (f *Firewall) Start() error {
	f.applyRules()
	go func() {
		for {
			if err := f.WatchConfigFile(); err != nil {
				log.Errorf("error watching config file: %v", err)
			}
		}
	}()
	go f.grpcProxy.Start()
	return f.lcdProxy.Start()
}

func (f *Firewall) WatchConfigFile() error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()

	if err := watcher.Add(filepath.Dir(f.cfgFile)); err != nil {
		return err
	}
	for {
		select {
		case _, ok := <-watcher.Events:
			if !ok {
				return fmt.Errorf("could not retrieve event")
			}
			log.Info("reloading config file")
			if err := f.loadConfig(); err != nil {
				return err
			}
			f.applyRules()
		case err, ok := <-watcher.Errors:
			if !ok {
				return fmt.Errorf("could not retrieve error")
			}
			return err
		}
	}
}

func (f *Firewall) loadConfig() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	var err error
	f.cfg, err = ReadConfigFromFile(f.cfgFile)
	return err
}

func (f *Firewall) applyRules() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lcdProxy.SetRules(f.cfg.LCD.Rules, f.cfg.LCD.Default)
	f.grpcProxy.SetRules(f.cfg.GRPC.Rules, f.cfg.GRPC.Default)
}
