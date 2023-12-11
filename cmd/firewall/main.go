package main

import (
	"flag"
	"os"
	"path/filepath"

	log "github.com/sirupsen/logrus"

	"github.com/NibiruChain/cosmos-firewall/pkg/firewall"
)

const (
	defaultConfigFileName = "firewall.yaml"
)

func init() {
	homedir, _ := os.UserHomeDir()
	flag.StringVar(&configFile, "config", filepath.Join(homedir, defaultConfigFileName), "Path to configuration file.")
	flag.StringVar(&logLevel, "log-level", "info", "log level.")
	flag.StringVar(&logFormat, "log-format", "json", "log format (either json or text)")
}

var (
	configFile string
	logLevel   string
	logFormat  string
)

func main() {
	flag.Parse()
	logLvl, err := log.ParseLevel(logLevel)
	if err != nil {
		log.Fatal(err)
	}
	log.SetLevel(logLvl)
	if logFormat == "json" {
		log.SetFormatter(&log.JSONFormatter{})
	}

	f, err := firewall.New(configFile)
	if err != nil {
		log.Fatal(err)
	}
	log.Fatal(f.Start())
}
