package main

import (
	"os"
	"os/user"
	"path"
	"path/filepath"

	lxd "github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxc/config"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/version"
	logging "github.com/op/go-logging"
)

type Client struct {
	conf *config.Config
	d    lxd.ContainerServer
}

func loadConfig() *config.Config {
	var configDir string
	var conf *config.Config
	var err error

	if os.Getenv("LXD_CONF") != "" {
		configDir = os.Getenv("LXD_CONF")
	} else if os.Getenv("HOME") != "" {
		configDir = path.Join(os.Getenv("HOME"), ".config", "lxc")
	} else {
		user, err := user.Current()
		if err != nil {
			log.Criticalf("unable to get current user: %s", err)
			os.Exit(ErrConfig)
		}

		configDir = path.Join(user.HomeDir, ".config", "lxc")
	}
	configPath := os.ExpandEnv(path.Join(configDir, "config.yml"))

	if shared.PathExists(configPath) {
		conf, err = config.LoadConfig(configPath)
		if err != nil {
			log.Criticalf("unable to load config: %s", err)
			os.Exit(ErrConfig)
		}
	} else {
		conf = &config.DefaultConfig
		conf.ConfigDir = filepath.Dir(configPath)
	}

	// Set the user agent
	conf.UserAgent = version.UserAgent

	return conf
}

func getServer(conf *config.Config) lxd.ContainerServer {
	var remote string
	if remote == "" {
		remote = conf.DefaultRemote
	}

	d, err := conf.GetContainerServer(remote)
	if err != nil {
		log.Criticalf("unable to get server: %s", err)
		os.Exit(ErrServer)
	}

	return d
}

func startLogger() {
	logStderr := logging.NewLogBackend(os.Stderr, "", 0)
	logSyslog, err := logging.NewSyslogBackend(name)

	if err != nil {
		log.Criticalf("unable to open logging: %s", err)
		os.Exit(ErrLog)
	}

	logging.SetBackend(logStderr, logSyslog)
}

func InitClient() Client {
	config := loadConfig()
	return Client{
		conf: config,
		d:    getServer(config),
	}

}

func (c Client) GetContainers() map[string]api.Container {
	containers, err := c.d.GetContainers()

	if err != nil {
		log.Criticalf("error getting containers: %s", err)
		os.Exit(ErrServer)
	}

	cts := make(map[string]api.Container)
	for _, container := range containers {
		cts[container.Name] = container
	}

	return cts
}
