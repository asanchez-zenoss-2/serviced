/*******************************************************************************
* Copyright (C) Zenoss, Inc. 2014, all rights reserved.
*
* This content is made available according to terms specified in
* License.zenoss under the directory where your Zenoss product is installed.
*
*******************************************************************************/
package serviced

import (
	"encoding/json"
	"github.com/zenoss/glog"
	"github.com/zenoss/serviced/dao"
	"io/ioutil"
	"os"
	"strings"
	"testing"
	"time"
)

func TestGetLogstashBindMounts(t *testing.T) {
	bindMounts := getLogstashBindMounts("pepe")
	pieces := strings.Split(bindMounts, "-v")
	// the first part is the logstash stuff mount and the second
	// is the config file
	configMount := strings.TrimSpace(pieces[2])
	glog.V(1).Infof("configMount: %s", configMount)
	if configMount != "pepe:"+LOGSTASH_CONTAINER_CONFIG {
		t.Errorf("String: %s Was not equal to %s", configMount, "pepe:"+LOGSTASH_CONTAINER_CONFIG)
	}
}

func getTestService() dao.Service {
	return dao.Service{
		Id:              "0",
		Name:            "Zenoss",
		Context:         "",
		Startup:         "",
		Description:     "Zenoss 5.x",
		Instances:       0,
		ImageId:         "",
		PoolId:          "",
		DesiredState:    0,
		Launch:          "auto",
		Endpoints:       []dao.ServiceEndpoint{},
		ParentServiceId: "",
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
		LogConfigs: []dao.LogConfig{
			dao.LogConfig{
				Path: "/path/to/log/file",
				Type: "test",
				Tags: map[string]string{
					"pepe": "foobar",
				},
			},
			dao.LogConfig{
				Path: "/path/to/second/log/file",
				Type: "test2",
				Tags: map[string]string{
					"test": "tags",
				},
			},
		},
	}
}

func TestMakeSureTagsMakeItIntoTheJson(t *testing.T) {
	service := getTestService()
	confFileLocation, err := writeLogstashAgentConfig(&service)

	if err != nil {
		t.Errorf("Error writing config file %s", err)
		return
	}

	// go ahead and clean up
	defer func() {
		os.Remove(confFileLocation)
	}()
	contents, err := ioutil.ReadFile(confFileLocation)
	if err != nil {
		t.Errorf("Error reading config file %s", err)
		return
	}

	if !strings.Contains(string(contents), "\"pepe\":\"foobar\"") {
		t.Errorf("Tags did not make it into the config file %s", string(contents))
		return
	}
}

func TestMakeSureConfigIsValidJSON(t *testing.T) {
	service := getTestService()
	confFileLocation, err := writeLogstashAgentConfig(&service)

	if err != nil {
		t.Errorf("Error writing config file %s", err)
		return
	}

	// go ahead and clean up
	defer func() {
		os.Remove(confFileLocation)
	}()
	contents, err := ioutil.ReadFile(confFileLocation)
	if err != nil {
		t.Errorf("Error reading config file %s", err)
		return
	}

	var dat map[string]interface{}
	if err := json.Unmarshal(contents, &dat); err != nil {
		t.Errorf("Error decoding config file %s with err", string(contents), err)
		return
	}

	glog.V(1).Infof("encoded file %v", dat)

	// make sure path to logfile is present
	if !strings.Contains(string(contents), "path/to/log/file") {
		t.Errorf("The logfile path was not in the configuration", string(contents), err)
	}
}
