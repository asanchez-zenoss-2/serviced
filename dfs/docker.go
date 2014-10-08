// Copyright 2014 The Serviced Authors.
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package dfs

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/control-center/serviced/commons"
	"github.com/control-center/serviced/commons/docker"
	"github.com/control-center/serviced/commons/layer"
	"github.com/control-center/serviced/dao"
	"github.com/control-center/serviced/domain/service"
	"github.com/control-center/serviced/domain/servicedefinition"
	"github.com/control-center/serviced/domain/servicetemplate"
	"github.com/control-center/serviced/zzk"
	zkservice "github.com/control-center/serviced/zzk/service"
	"github.com/zenoss/glog"
	dockerclient "github.com/zenoss/go-dockerclient"
)

const (
	DockerLatest = "latest"
)

type imagemeta struct {
	UUID     string
	Tags     []string
	Filename string
}

// Commit will merge a container into existing services' image
func (dfs *DistributedFilesystem) Commit(dockerID string) (string, error) {
	// get the container and verify that it is not running
	ctr, err := docker.FindContainer(dockerID)
	if err != nil {
		glog.Errorf("Could not get container %s: %s", dockerID, err)
		return "", err
	}

	if ctr.IsRunning() {
		err := fmt.Errorf("cannot commit a running container")
		glog.Errorf("Error committing container %s: %s", ctr.ID, err)
		return "", err
	}

	// parse the image information
	imageID, err := commons.ParseImageID(ctr.Config.Image)
	if err != nil {
		glog.Errorf("Could not parse image information for %s: %s", dockerID, err)
		return "", err
	}
	tenantID := imageID.User

	// find the image that is being committed
	image, err := findImage(tenantID, ctr.Image, DockerLatest)
	if err != nil {
		glog.Errorf("Could not find image %s: %s", dockerID, err)
		return "", fmt.Errorf("cannot commit a stale container")
	}

	// check the number of image layers
	if layers, err := image.History(); err != nil {
		glog.Errorf("Could not check history for image %s: %s", image.ID, err)
		return "", err
	} else if numLayers := len(layers); numLayers >= layer.WARN_LAYER_COUNT {
		glog.Warningf("Image %s has %d layers and is approaching the maximum (%d). Please squash image layers.",
			image.ID, numLayers, layer.MAX_LAYER_COUNT)
	} else {
		glog.V(3).Infof("Image %s has %d layers", image.ID, numLayers)
	}

	// commit the container to the image and tag
	if _, err := ctr.Commit(image.ID.BaseName()); err != nil {
		glog.Errorf("Error trying to commit %s to %s: %s", dockerID, image.ID, err)
		return "", err
	}

	// desynchronize any running containers
	if err := dfs.desynchronize(image.ID, time.Now()); err != nil {
		glog.Warningf("Could not denote all desynchronized services: %s", err)
	}

	// snapshot the filesystem and images
	snapshotID, err := dfs.Snapshot(tenantID)
	if err != nil {
		glog.Errorf("Could not create a snapshot of the new image %s: %s", tenantID, err)
		return "", err
	}

	return snapshotID, nil
}

func (dfs *DistributedFilesystem) desynchronize(imageID commons.ImageID, commit time.Time) error {
	var svcs []service.Service
	if err := dfs.client.GetServices(dao.ServiceRequest{}, &svcs); err != nil {
		glog.Errorf("Could not get all services", err)
		return err
	}

	for _, svc := range svcs {
		conn, err := zzk.GetLocalConnection(zzk.GeneratePoolPath(svc.PoolID))
		if err != nil {
			glog.Errorf("Could not acquire connection to coordinator (%s): %s", svc.PoolID, err)
			return err
		}

		// figure out which services use the provided image
		img, err := commons.ParseImageID(svc.ImageID)
		if err != nil {
			glog.Errorf("Error while parsing image %s for %s (%s): %s", svc.ImageID, svc.Name, svc.ID)
			return err
		}

		if !img.Equals(imageID) {
			continue
		}

		states, err := zkservice.GetServiceStates(conn, svc.ID)
		if err != nil {
			glog.Errorf("Could not get running services for %s (%s): %s", svc.Name, svc.ID)
			return err
		}

		for _, state := range states {
			// check if the instance has been running since before the commit
			if state.IsRunning() && state.Started.Before(commit) {
				state.InSync = false
				if err := zkservice.UpdateServiceState(conn, &state); err != nil {
					glog.Errorf("Could not update service state %s for %s (%s) as out of sync: %s", state.ID, svc.Name, svc.ID, err)
					return err
				}
			}
		}
	}
	return nil
}

func (dfs *DistributedFilesystem) exportImages(dirpath string, templates map[string]servicetemplate.ServiceTemplate, services []service.Service) ([]imagemeta, error) {
	tRepos, sRepos := getImageRefs(templates, services)
	imageTags, err := getImageTags(tRepos, sRepos)
	if err != nil {
		return nil, err
	}

	registry := fmt.Sprintf("%s:%d", dfs.dockerHost, dfs.dockerPort)
	i := 0
	var result []imagemeta
	for uuid, tags := range imageTags {
		metadata := imagemeta{Filename: fmt.Sprintf("%d.tar", i), UUID: uuid, Tags: tags}

		filename := filepath.Join(dirpath, metadata.Filename)
		// Try to find the tag referring to the local registry, so we don't
		// make a call to Docker Hub potentially with invalid auth
		// Default to the first tag in the list
		if len(tags) == 0 {
			continue
		}

		tag := tags[0]
		for _, t := range tags {
			if strings.HasPrefix(t, registry) {
				tag = t
				break
			}
		}

		if err := saveImage(tag, filename); err == dockerclient.ErrNoSuchImage {
			glog.Warningf("Docker image %s was referenced, but does not exist. Skipping.", tag)
			continue
		} else if err != nil {
			glog.Errorf("Could not export %s: %s", tag, err)
			return nil, err
		}
		result = append(result, metadata)
		i++
	}
	return result, nil
}

func (dfs *DistributedFilesystem) importImages(dirpath string, images []imagemeta, tenants map[string]struct{}) error {
	for _, metadata := range images {
		filename := filepath.Join(dirpath, metadata.Filename)

		// Make sure all images that refer to a local registry are named with the local registry
		tags := make([]string, len(metadata.Tags))
		for i, tag := range metadata.Tags {
			imageID, err := commons.ParseImageID(tag)
			if err != nil {
				glog.Errorf("Could not parse %s: %s", tag, err)
				return err
			}

			if _, ok := tenants[imageID.User]; ok {
				imageID.Host, imageID.Port = dfs.dockerHost, dfs.dockerPort
			}
			tags[i] = imageID.String()
		}

		if err := loadImage(filename, metadata.UUID, tags); err != nil {
			glog.Errorf("Error loading %s (%s): %s", filename, metadata.UUID, err)
			return err
		}
	}
	return nil
}

func findImage(tenantID, uuid, tag string) (*docker.Image, error) {
	images, err := docker.Images()
	if err != nil {
		return nil, err
	}

	for _, image := range images {
		if image.ID.User == tenantID && image.UUID == uuid && image.ID.Tag == tag {
			return image, nil
		}
	}

	return nil, fmt.Errorf("image not found")
}

func findImages(tenantID, tag string) ([]*docker.Image, error) {
	images, err := docker.Images()
	if err != nil {
		return nil, err
	}

	var result []*docker.Image
	for _, image := range images {
		if image.ID.Tag == tag && image.ID.User == tenantID {
			result = append(result, image)
		}
	}
	return result, nil
}

func searchImagesByTenantID(tenantID string) ([]*docker.Image, error) {
	images, err := docker.Images()
	if err != nil {
		return nil, err
	}

	var result []*docker.Image
	for i, image := range images {
		if image.ID.User == tenantID {
			result = append(result, images[i])
		}
	}
	return result, nil
}

func tag(tenantID, oldtag, newtag string) error {
	images, err := findImages(tenantID, oldtag)
	if err != nil {
		return err
	}

	var tagged []*docker.Image
	for _, image := range images {
		t, err := image.Tag(fmt.Sprintf("%s:%s", image.ID.BaseName(), newtag))
		if err != nil {
			glog.Errorf("Error while adding tags; rolling back: %s", err)
			for _, t := range tagged {
				if err := t.Delete(); err != nil {
					glog.Errorf("Could not untag image %s: %s", t.ID, err)
				}
			}
			return err
		}
		tagged = append(tagged, t)
	}
	return nil
}

func getImageTags(templateRepos []string, serviceRepos []string) (map[string][]string, error) {
	// make a map of all docker images
	images, err := docker.Images()
	if err != nil {
		return nil, err
	}

	// TODO: enable tagmap if we are storing all snapshots in a backup
	// tagmap := make(map[string][]string)
	imap := make(map[string]string)

	for _, image := range images {

		if image.ID.Tag == DockerLatest {
			image.ID.Tag = ""
		}
		// repo := image.ID.BaseName()
		// tagmap[repo] = append(tagmap[repo], image.ID.String())
		imap[image.ID.String()] = image.UUID
	}

	repos := append(templateRepos, serviceRepos...)

	// TODO: Enable this if we are storing all snapshots in a backup
	// Get all the tags related to a service
	/*
		repos := templateRepos
		for _, repo := range serviceRepos {
			imageID, err := commons.ParseImageID(repo)
			if err != nil {
				glog.Errorf("Invalid image %s: %s", repo, err)
				return nil, err
			}
			repos = append(repos, tagmap[imageID.BaseName()]...)
		}
	*/

	// Organize repos by UUID
	result := make(map[string][]string)
	for _, repo := range repos {
		if imageID, ok := imap[repo]; ok {
			result[imageID] = append(result[imageID], repo)
		} else {
			err := fmt.Errorf("not found: %s", repo)
			return nil, err
		}
	}

	return result, nil
}

func getImageRefs(templates map[string]servicetemplate.ServiceTemplate, services []service.Service) (t []string, s []string) {
	tmap := make(map[string]struct{})
	smap := make(map[string]struct{})

	var visit func(*[]servicedefinition.ServiceDefinition)
	visit = func(sds *[]servicedefinition.ServiceDefinition) {
		for _, sd := range *sds {
			if sd.ImageID != "" {
				tmap[sd.ImageID] = struct{}{}
			}
			visit(&sd.Services)
		}
	}

	for _, template := range templates {
		visit(&template.Services)
	}
	for _, service := range services {
		if service.ImageID != "" {
			smap[service.ImageID] = struct{}{}
		}
	}

	for r := range tmap {
		t = append(t, r)
	}

	for r := range smap {
		s = append(s, r)
	}

	return t, s
}

func saveImage(imageID, filename string) error {
	file, err := os.Create(filename)
	if err != nil {
		glog.Errorf("Could not create file %s: %s", filename, err)
		return err
	}

	defer func() {
		if err := file.Close(); err != nil {
			glog.Warningf("Could not close file %s: %s", filename, err)
		}
	}()

	cd := &docker.ContainerDefinition{
		dockerclient.CreateContainerOptions{
			Config: &dockerclient.Config{
				Cmd:   []string{"echo"},
				Image: imageID,
			},
		},
		dockerclient.HostConfig{},
	}

	ctr, err := docker.NewContainer(cd, false, 10*time.Second, nil, nil)
	if err != nil {
		glog.Errorf("Could not create container from image %s.  Have you synced lately?  (serviced docker sync): %s", imageID, err)
		return err
	}

	glog.V(1).Infof("Created container %s based on image %s", ctr.ID, imageID)
	defer func() {
		if err := ctr.Delete(true); err != nil {
			glog.Errorf("Could not remove container %s (%s): %s", ctr.ID, imageID, err)
		}
	}()

	if err := ctr.Export(file); err != nil {
		glog.Errorf("Could not export container %s (%s): %v", ctr.ID, imageID, err)
		return err
	}

	glog.Infof("Exported container %s (based on image %s) to %s", ctr.ID, imageID, filename)
	return nil
}

func loadImage(filename string, uuid string, tags []string) error {
	// look up the image by UUID
	images, err := docker.Images()
	if err != nil {
		glog.Errorf("Could not look up images: %s", err)
		return err
	}

	var image *docker.Image
	for _, i := range images {
		if i.UUID == uuid {
			image = i
			break
		}
	}

	// image not found so import
	if image == nil {
		glog.Warningf("Importing image from file, don't forget to sync (serviced docker sync)")
		if err := docker.ImportImage(tags[0], filename); err != nil {
			glog.Errorf("Could not import image from file %s: %s", filename, err)
			return err
		} else if image, err = docker.FindImage(tags[0], false); err != nil {
			glog.Errorf("Could not look up docker image %s: %s", tags[0], err)
			return err
		}
		glog.Infof("Tagging images %v at %s", tags, image.UUID)
		tags = tags[1:]
	}

	// tag the remaining images
	for _, tag := range tags {
		if _, err := image.Tag(tag); err != nil {
			glog.Errorf("Could not tag image %s as %s: %s", image.UUID, tag, err)
			return err
		}
	}

	return nil
}

// createVolumeDir() creates a directory on the running host using the user ids
// found within the specified image. For example, it can create a directory owned
// by the mysql user (as seen by the container) despite there being no mysql user
// on the host system.
// Assumes that the local docker image (imageSpec) exists and has been sync'd
// with the registry.
func createVolumeDir(hostPath, containerSpec, imageSpec, userSpec, permissionSpec string) error {
	// FIXME: this relies on the underlying container to have /bin/sh that supports
	// some advanced shell options. This should be rewriten so that serviced injects itself in the
	// container and performs the operations using only go!
	// the file globbing checks that /mnt/dfs is empty before the copy - should initially be empty
	//    we don't want the copy to occur multiple times if restarting services.

	var err error
	var output []byte
	command := [...]string{
		"docker", "run",
		"--rm",
		"-v", hostPath + ":/mnt/dfs",
		imageSpec,
		"/bin/bash", "-c",
		fmt.Sprintf(`
			chown %s /mnt/dfs && \
			chmod %s /mnt/dfs && \
			shopt -s nullglob && \
			shopt -s dotglob && \
			files=(/mnt/dfs/*) && \
			if [ ! -d "%s" ]; then
				echo "ERROR: srcdir %s does not exist in container"
				exit 2
			elif [ ${#files[@]} -eq 0 ]; then
				cp -rp %s/* /mnt/dfs/
			fi
			sleep 5s
			`, userSpec, permissionSpec, containerSpec, containerSpec, containerSpec),
	}

	for i := 0; i < 1; i++ {
		docker := exec.Command(command[0], command[1:]...)
		output, err = docker.CombinedOutput()
		if err == nil {
			return nil
		}
		time.Sleep(time.Second)
	}

	glog.Errorf("could not create host volume: %+v, %s", command, string(output))
	return err
}

func bindcopy(hostPath, containerPath, imageID, user, permission string) error {
	const mount string = "/mnt/dfs"

	var cd docker.ContainerDefinition
	cd.Config = &dockerclient.Config{
		Image:   imageID,
		Volumes: map[string]struct{}{mount: struct{}{}},
		Cmd: []string{"/bin/bash", "-c", fmt.Sprintf(`
			chown %s /mnt/dfs && \
			chmod %s /mnt/dfs && \
			shopt -s nullglob && \
			shopt -s dotglob && \
			files=(/mnt/dfs/*) && \
			if [ ! -d "%s" ]; then
				echo "ERROR: srcdir %s does not exist in container"
				exit 2
			elif [ ${#files[@]} -eq 0 ]; then
				cp -rp %s/* /mnt/dfs/
			fi
			sleep 5s
		`, user, permission, containerPath, containerPath, containerPath)},
	}
	cd.HostConfig = dockerclient.HostConfig{
		Binds: []string{fmt.Sprintf("%s:%s", hostPath, mount)},
	}

	ctr, err := docker.NewContainer(&cd, false, 30*time.Second, nil, nil)
	if err != nil {
		glog.Errorf("Could not create container (%s) for bind copy: %s", imageID, err)
		return err
	}

	if err := ctr.Start(30 * time.Second); err != nil {
		glog.Errorf("Could not start container %s (%s): %s", ctr.ID, imageID, err)
		return err
	}

	xc, err := ctr.Wait(5 * time.Minute)

	if err == docker.ErrRequestTimeout {
		glog.Errorf("Container %s (%s) timed out: %s", ctr.ID, imageID, err)
		return err
	} else if err != nil {
		glog.Errorf("Could not bind copy %s (%s): %s", ctr.ID, imageID, err)
		return err
	}

	if xc != 0 {
		glog.Warningf("Bind copy returned with exit code %d", xc)
	}

	return nil
}
