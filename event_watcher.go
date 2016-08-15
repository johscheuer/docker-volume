package main

import (
	"fmt"
	"log"
	"path"
	"strings"

	"github.com/fsouza/go-dockerclient"
)

var (
	LabelQuobytePrefix = "quobyte."
	LabelUser          = LabelQuobytePrefix + "user"
	LabelGroup         = LabelQuobytePrefix + "group"
)

var validLabels = []string{
	LabelUser,
	LabelGroup,
}

type watcher struct {
	WatchedStatus       map[string]bool
	WatchedLabelsPrefix string

	dockerClient *docker.Client
	listener     chan *docker.APIEvents
}

func newWatcher(d *docker.Client) (*watcher, error) {
	return &watcher{
		WatchedStatus:       map[string]bool{"create": true},
		WatchedLabelsPrefix: LabelQuobytePrefix,
		dockerClient:        d,
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

	labels := watcher.watchedLabels(c)
	if len(labels) == 0 {
		return nil
	}

	return adjustMounts(c, labels)
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

func adjustMounts(c *docker.Container, labels map[string]string) error {
	//mounts := []docker.Mount
	for i, mount := range c.Mounts {
		if mount.Driver != "quobyte" {
			continue
		}

		mountDir, mountVolume := path.Split(mount.Source)
		if user, ok := labels[LabelUser]; ok {
			if group, ok := labels[LabelGroup]; ok {
				mount.Source = path.Join(mountDir, fmt.Sprintf("%s#%s@%s", user, group, mountVolume))
				c.Mounts[i] = mount
				continue
			}

			mount.Source = path.Join(mountDir, fmt.Sprintf("%s@%s", user, mountVolume))
			c.Mounts[i] = mount
		}
	}

	log.Printf("Container: %v", c)
	return nil
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
