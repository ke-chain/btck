package main

import (
	"path/filepath"

	"github.com/btcsuite/btcutil"
)

const (
	defaultLogDirname  = "logs"
	defaultLogFilename = "btcd.log"
)

var (
	defaultHomeDir = btcutil.AppDataDir("btcd", false)
	defaultLogDir  = filepath.Join(defaultHomeDir, defaultLogDirname)
)

type config struct {
	LogDir            string   `long:"logdir" description:"Directory to log output."`
	UserAgentComments []string `long:"uacomment" description:"Comment to add to the user agent -- See BIP 14 for more information."`
}

// loadConfig initializes and parses the config using a config file and command
// line options.
//
// The configuration proceeds as follows:
// 	1) Start with a default config with sane settings
// 	2) Pre-parse the command line to check for an alternative config file
// 	3) Load configuration file overwriting defaults with any specified options
// 	4) Parse CLI options and overwrite/add any specified options
//
// The above results in btcd functioning properly without any config settings
// while still allowing the user to override settings with config files and
// command line options.  Command line options always take precedence.
func loadConfig() (*config, []string, error) {
	// Default config.
	cfg := config{
		LogDir: defaultLogDir,
	}
	// Initialize log rotation.  After log rotation has been initialized, the
	// logger variables may be used.
	initLogRotator(filepath.Join(cfg.LogDir, defaultLogFilename))
	return &cfg, nil, nil
}
