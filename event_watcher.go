package main

import (
	"fmt"
	"log"
	"path"
	"strings"

	"github.com/fsouza/go-dockerclient"
)

var (
	labelQuobytePrefix = "quobyte."
	labelUser          = labelQuobytePrefix + "user"
	labelGroup         = labelQuobytePrefix + "group"
)

var validLabels = []string{
	labelUser,
	labelGroup,
}

type watcher struct {
	WatchedStatus       map[string]bool
	WatchedLabelsPrefix string

	dockerClient       *docker.Client
	listener           chan *docker.APIEvents
	recreatedContainer map[string]bool
}

func newWatcher(d *docker.Client) (*watcher, error) {
	return &watcher{
		WatchedStatus:       map[string]bool{"create": true},
		WatchedLabelsPrefix: labelQuobytePrefix,
		dockerClient:        d,
		recreatedContainer:  make(map[string]bool),
	}, nil
}

func (watcher *watcher) Watch() error {
	watcher.listener = make(chan *docker.APIEvents, 0)

	if err := watcher.dockerClient.AddEventListener(watcher.listener); err != nil {
		return err
	}

	for e := range watcher.listener {
		if err := watcher.handleEvent(e); err != nil {
			log.Printf("error handling event container %s error: %s", e.ID[:12], err)
		}
	}

	return nil
}

func (watcher *watcher) handleEvent(e *docker.APIEvents) error {
	if !watcher.WatchedStatus[e.Status] {
		return nil
	}

	c, err := watcher.dockerClient.InspectContainer(e.ID)
	if err != nil {
		return err
	}

	if _, ok := watcher.recreatedContainer[c.ID]; ok == true {
		log.Printf("Container %s already newly created", c.ID)
		return nil
	}

	labels := watcher.watchedLabels(c)
	if len(labels) == 0 {
		return nil
	}

	return watcher.adjustMounts(c, labels)
}

func (watcher *watcher) watchedLabels(c *docker.Container) map[string]string {
	var matched = make(map[string]string, 0)
	for label, value := range c.Config.Labels {
		if !strings.HasPrefix(label, watcher.WatchedLabelsPrefix) {
			continue
		}

		matched[label] = value
	}

	return matched
}

func (watcher *watcher) adjustMounts(c *docker.Container, labels map[string]string) error {
	for i, mount := range c.Mounts {
		if mount.Driver != "quobyte" {
			continue
		}

		mountDir, mountVolume := path.Split(mount.Source)
		if user, ok := labels[labelUser]; ok {
			if group, ok := labels[labelGroup]; ok {
				mount.Source = path.Join(mountDir, fmt.Sprintf("%s#%s@%s", user, group, mountVolume))
			} else {
				mount.Source = path.Join(mountDir, fmt.Sprintf("%s@%s", user, mountVolume))
			}
		} else {
			mount.Source = path.Join(mountDir, fmt.Sprintf("%s@%s", "root", mountVolume))
		}
		c.Mounts[i] = mount
	}

	return watcher.recreateContainer(c)
}

func (watcher *watcher) recreateContainer(c *docker.Container) error {
	c.Config.Mounts = c.Mounts
	newContainer, err := watcher.dockerClient.CreateContainer(
		docker.CreateContainerOptions{
			Config:     c.Config,
			HostConfig: c.HostConfig,
		})

	if err != nil {
		return err
	}

	watcher.recreatedContainer[newContainer.ID] = true

	if err := watcher.dockerClient.RemoveContainer(
		docker.RemoveContainerOptions{
			ID:            c.ID,
			RemoveVolumes: false,
			Force:         true}); err != nil {
		return err
	}

	return watcher.dockerClient.StartContainer(newContainer.ID, newContainer.HostConfig)
}

func runWatcher() error {
	log.Println("Starting watcher")
	d, err := docker.NewClientFromEnv()
	if err != nil {
		return fmt.Errorf("Error creating docker client: %s", err)
	}

	w, err := newWatcher(d)
	if err != nil {
		return fmt.Errorf("error creating watcher: %s", err)
	}

	if err := w.Watch(); err != nil {
		return fmt.Errorf("error starting watcher: %s", err)
	}

	return nil
}
