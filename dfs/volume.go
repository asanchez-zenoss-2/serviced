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
	"errors"
	"fmt"
	"os"
	"os/user"
	"path"
	"path/filepath"
	"strings"

	"github.com/control-center/serviced/domain/service"
	"github.com/control-center/serviced/volume"
	"github.com/zenoss/glog"
)

func (dfs *DistributedFilesystem) GetVolume(serviceID string) (*volume.Volume, error) {
	v, err := getSubvolume(dfs.vfs, dfs.varpath, serviceID)
	if err != nil {
		glog.Errorf("Could not acquire subvolume for service %s: %s", serviceID, err)
		return nil, err
	} else if v == nil {
		err := errors.New("volume is nil")
		glog.Errorf("Could not get volume for service %s: %s", serviceID, err)
		return nil, err
	}

	return v, nil
}

func (dfs *DistributedFilesystem) GetBindMounts(svc *service.Service, source *volume.Volume) (map[string]string, error) {
	bindmounts := make(map[string]string)
	for _, volume := range svc.Volumes {
		if !(volume.Type == "" || volume.Type == "dfs") {
			continue
		}

		resourcepath := filepath.Join(source.Path(), volume.ResourcePath)
		if err := os.MkdirAll(resourcepath, 0770); err != nil {
			glog.Errorf("Could not create resource path %s for %s (%s): %s", resourcepath, svc.Name, svc.ID)
			return nil, err
		}

		if err := bindcopy(resourcepath, volume.ContainerPath, svc.ImageID, volume.Owner, volume.Permission); err != nil {
			glog.Errorf("Error populating resource path (%s) with container path (%s): %s", resourcepath, volume.ContainerPath, err)
			return nil, err
		}

		bindmounts[resourcepath] = volume.ContainerPath
	}

	return bindmounts, nil
}

func getSubvolume(vfs, varpath, serviceID string) (*volume.Volume, error) {
	baseDir, err := filepath.Abs(path.Join(varpath, "volumes"))
	if err != nil {
		return nil, err
	}
	glog.Infof("Mounting vfs: %v; tenantID: %v; baseDir: %v", vfs, serviceID, baseDir)
	return volume.Mount(vfs, serviceID, baseDir)
}

func getHome() string {
	if servicedHome := strings.TrimSpace(os.Getenv("SERVICED_HOME")); servicedHome != "" {
		return servicedHome
	} else if user, err := user.Current(); err != nil {
		return path.Join(os.TempDir(), fmt.Sprintf("serviced-%s", user.Username))
	}

	home := path.Join(os.TempDir(), "serviced")
	glog.Warningf("Defaulting home to %s", home)
	return home
}
