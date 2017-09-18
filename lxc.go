package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
	"path"
	"path/filepath"
	"strings"

	lxd "github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxc/config"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/version"

	"github.com/sirupsen/logrus"

	versioncmp "github.com/mcuadros/go-version"
)

type Client struct {
	conf    *config.Config
	d       lxd.ContainerServer
	version string
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
			logrus.WithError(err).
				Fatal("unable to get current user")
		}

		configDir = path.Join(user.HomeDir, ".config", "lxc")
	}
	configPath := os.ExpandEnv(path.Join(configDir, "config.yml"))

	if shared.PathExists(configPath) {
		conf, err = config.LoadConfig(configPath)
		if err != nil {
			logrus.WithError(err).Fatal("unable to load config")
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
		logrus.WithError(err).Fatal("unable to get server")
	}

	return d
}

func InitClient() Client {
	config := loadConfig()

	server := getServer(config)
	serverEnv, _, err := server.GetServer()

	if err != nil {
		log.WithError(err).Fatal("could not read server environment")
	}

	return Client{
		conf:    config,
		d:       server,
		version: serverEnv.Environment.ServerVersion,
	}

}

func (c Client) GetContainers() map[string]api.Container {
	containers, err := c.d.GetContainers()

	if err != nil {
		logrus.WithError(err).
			Fatal("error getting containers")
	}

	cts := make(map[string]api.Container)
	for _, container := range containers {
		cts[container.Name] = container
	}

	return cts
}

func (c Client) GetContainerCopyArgs() lxd.ContainerCopyArgs {

	args := lxd.ContainerCopyArgs{
		// Default value as of lxc 2.17 is "pull" -- we'll use that
		Mode: "pull",
		// Don't copy stateful; no need to dump memory
		Live: false,
	}

	// We don't want to copy any snapshots, just the running
	// instance.
	//
	// `ContainerOnly` is only supported from LXD 2.17:
	// https://github.com/lxc/lxd/commit/b24663294ff2a4492a7858f89745581c0e418cde
	args.ContainerOnly = versioncmp.CompareSimple(c.version, "2.17") >= 0

	return args
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
func LXCPullFile(log *logrus.Entry, ct *api.Container, remote string, sources []string, target string) (err error) {
	log = log.WithFields(logrus.Fields{
		"remote": remote,
		"target": target,
	})

	if len(sources) == 0 || target == "" {
		err := errors.New("invalid source or target")
		log.Error(err)
		return err
	}

	// clean various /./, /../, /////, etc. that users add (#2557)
	targetPath := path.Clean(target)
	targetIsDir := strings.HasSuffix(targetPath, "/")

	if len(sources) > 1 && targetIsDir {
		return errors.New("more than one source proviced, but target is not a dir")
	}

	log.WithFields(logrus.Fields{
		"targetPath":  targetPath,
		"targetIsDir": targetIsDir,
	}).Debug()

	type CopyFile struct {
		src     *os.File
		dstResp *lxd.ContainerFileResponse
		dstBuf  io.Reader
	}

	/* Make sure all of the files are accessible by us before trying to
	 * push any of them. */
	files := make(map[string]CopyFile)
	for _, filename := range sources {
		if filename == "-" {
			err := errors.New("can't use stdin as a source")
			log.Error(err)
			return err
		}

		destFilename := path.Join(targetPath, path.Base(filename))

		log = log.WithFields(logrus.Fields{
			"filename":     filename,
			"destFilename": destFilename,
		})

		buf, resp, err := client.d.GetContainerFile(remote, filename)
		if err != nil {
			err = fmt.Errorf("could not open file in src container: %s", err)
			log.WithError(err).Error()
			return err
		}

		if resp.Type == "directory" {
			err = errors.New("source should not be a directory")
			log.WithError(err).Error()
			return err
		}

		f, err := os.OpenFile(destFilename, os.O_CREATE|os.O_EXCL|os.O_WRONLY,
			os.FileMode(resp.Mode))

		if err != nil {
			err = fmt.Errorf("could not open target file for exclusive writing: %s", err)
			log.WithError(err).Error()
			return err
		}

		files[filename] = CopyFile{
			src:     f,
			dstResp: resp,
			dstBuf:  buf,
		}
	}

	for filename, cf := range files {
		log.WithFields(logrus.Fields{
			"filename": filename,
		}).Debug("copying file")

		_, err = io.Copy(cf.src, cf.dstBuf)
		if err != nil {
			err = fmt.Errorf("could not copy fileL %s", err)
			log.WithError(err).Error()
			return err
		}
	}

	return nil
}
