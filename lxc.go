package main

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	lxd "github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxc/config"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/i18n"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/version"

	log "github.com/sirupsen/logrus"
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
			log.WithError(err).
				Fatal("unable to get current user")
		}

		configDir = path.Join(user.HomeDir, ".config", "lxc")
	}
	configPath := os.ExpandEnv(path.Join(configDir, "config.yml"))

	if shared.PathExists(configPath) {
		conf, err = config.LoadConfig(configPath)
		if err != nil {
			log.WithError(err).Fatal("unable to load config")
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
		log.WithError(err).Fatal("unable to get server")
	}

	return d
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
		log.WithError(err).
			Fatal("error getting containers")
	}

	cts := make(map[string]api.Container)
	for _, container := range containers {
		cts[container.Name] = container
	}

	return cts
}

// Taken from
// https://github.com/lxc/lxd/blob/b5678b80f32d2de619c88009a518bbdfca21d9d8/lxc/file.go
type FileCmd struct {
	uid  int
	gid  int
	mode string

	recursive bool

	mkdirs bool
}

// Adapted from
// https://github.com/lxc/lxd/blob/b5678b80f32d2de619c88009a518bbdfca21d9d8/lxc/file.go
func LXCPushFile(c *FileCmd, conf *config.Config, sendFilePerms bool, args []string) error {
	if len(args) < 2 {
		return errors.New("invalid number of args")
	}

	target := args[len(args)-1]
	pathSpec := strings.SplitN(target, "/", 2)

	if len(pathSpec) != 2 {
		return fmt.Errorf(i18n.G("Invalid target %s"), target)
	}

	remote, container, err := conf.ParseRemote(pathSpec[0])
	if err != nil {
		return err
	}

	targetIsDir := strings.HasSuffix(target, "/")
	// re-add leading / that got stripped by the SplitN
	targetPath := "/" + pathSpec[1]
	// clean various /./, /../, /////, etc. that users add (#2557)
	targetPath = path.Clean(targetPath)

	// normalization may reveal that path is still a dir, e.g. /.
	if strings.HasSuffix(targetPath, "/") {
		targetIsDir = true
	}

	logger.Debugf("Pushing to: %s  (isdir: %t)", targetPath, targetIsDir)

	d, err := conf.GetContainerServer(remote)
	if err != nil {
		return err
	}

	var sourcefilenames []string
	for _, fname := range args[:len(args)-1] {
		if !strings.HasPrefix(fname, "--") {
			sourcefilenames = append(sourcefilenames, fname)
		}
	}

	mode := os.FileMode(0755)
	if c.mode != "" {
		if len(c.mode) == 3 {
			c.mode = "0" + c.mode
		}

		m, err := strconv.ParseInt(c.mode, 0, 0)
		if err != nil {
			return err
		}
		mode = os.FileMode(m)
	}

	uid := 0
	if c.uid >= 0 {
		uid = c.uid
	}

	gid := 0
	if c.gid >= 0 {
		gid = c.gid
	}

	if (len(sourcefilenames) > 1) && !targetIsDir {
		return errors.New("target is not a dir")
	}

	/* Make sure all of the files are accessible by us before trying to
	 * push any of them. */
	var files []*os.File
	for _, f := range sourcefilenames {
		var file *os.File
		if f == "-" {
			file = os.Stdin
		} else {
			file, err = os.Open(f)
			if err != nil {
				return err
			}
		}

		defer file.Close()
		files = append(files, file)
	}

	for _, f := range files {
		fpath := targetPath
		if targetIsDir {
			fpath = path.Join(fpath, path.Base(f.Name()))
		}

		args := lxd.ContainerFileArgs{
			Content: f,
			UID:     -1,
			GID:     -1,
			Mode:    -1,
		}

		if sendFilePerms {
			if c.mode == "" || c.uid == -1 || c.gid == -1 {
				finfo, err := f.Stat()
				if err != nil {
					return err
				}

				fMode, fUid, fGid := shared.GetOwnerMode(finfo)
				if err != nil {
					return err
				}

				if c.mode == "" {
					mode = fMode
				}

				if c.uid == -1 {
					uid = fUid
				}

				if c.gid == -1 {
					gid = fGid
				}
			}

			args.UID = int64(uid)
			args.GID = int64(gid)
			args.Mode = int(mode.Perm())
		}

		err = d.CreateContainerFile(container, fpath, args)
		if err != nil {
			return err
		}
	}

	return nil
}
