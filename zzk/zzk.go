// Copyright 2014 The Serviced Authors.
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package zzk

import (
	"errors"
	"path"
	"sync"

	"github.com/control-center/serviced/coordinator/client"
	"github.com/zenoss/glog"
)

// Errors
var (
	ErrTimeout  = errors.New("connection timeout")
	ErrShutdown = errors.New("listener shutdown")
)

// HostLeader is the node to store leader information for a host
type HostLeader struct {
	HostID  string
	version interface{}
}

// Version implements client.Node
func (node *HostLeader) Version() interface{} { return node.version }

// SetVersion implements client.Node
func (node *HostLeader) SetVersion(version interface{}) { node.version = version }

// NewHostLeader initializes a new host leader
func NewHostLeader(conn client.Connection, hostID, path string) client.Leader {
	return conn.NewLeader(path, &HostLeader{HostID: hostID})
}

// GetHostID finds the host of a led node
func GetHostID(leader client.Leader) (string, error) {
	var hl HostLeader
	if err := leader.Current(&hl); err != nil {
		return "", err
	}
	return hl.HostID, nil
}

// Listener is zookeeper node listener type
type Listener interface {
	// GetConnection expects a client.Connection object
	GetConnection() client.Connection
	// GetPath concatenates the base path with whatever child nodes that are specified
	GetPath(nodes ...string) string
	// Ready verifies that the listener can start listening
	Ready() error
	// Done performs any cleanup when the listener exits
	Done()
	// Spawn is the action to be performed when a child node is found on the parent
	Spawn(<-chan interface{}, string)
	// PostProcessing performs any additional actions given the nodes in process
	PostProcess(processing map[string]struct{})
}

// PathExists verifies if a path exists and does not raise an exception if the
// path does not exist
func PathExists(conn client.Connection, p string) (bool, error) {
	exists, err := conn.Exists(p)
	if err == client.ErrNoNode {
		return false, nil
	}
	return exists, err
}

// Ready waits for a node to be available for watching
func Ready(shutdown <-chan interface{}, conn client.Connection, p string) error {
	if exists, err := PathExists(conn, p); err != nil {
		return err
	} else if exists {
		return nil
	}

	for {
		if err := Ready(shutdown, conn, path.Dir(p)); err != nil {
			return err
		} else if exists, err := PathExists(conn, p); err != nil {
			return err
		} else if exists {
			return nil
		}
		_, event, err := conn.ChildrenW(path.Dir(p))
		if err != nil {
			return err
		}
		select {
		case <-event:
			// pass
		case <-shutdown:
			return ErrShutdown
		}
	}
}

// Listen initializes a listener for a particular zookeeper node
// shutdown:	signal to shutdown the listener
// ready:		signal to indicate that the listener has started watching its
//				child nodes (must set buffer size >= 1)
// l:			object that manages the zk interface for a specific path
func Listen(shutdown <-chan interface{}, ready chan<- error, l Listener) {
	var (
		_shutdown  = make(chan interface{})
		done       = make(chan string)
		processing = make(map[string]struct{})
		conn       = l.GetConnection()
	)

	glog.Infof("Starting a listener at %s", l.GetPath())
	if err := Ready(shutdown, conn, l.GetPath()); err != nil {
		glog.Errorf("Could not start listener at %s: %s", l.GetPath(), err)
		ready <- err
		return
	} else if err := l.Ready(); err != nil {
		glog.Errorf("Could not start listener at %s: %s", l.GetPath(), err)
		ready <- err
		return
	}

	close(ready)

	defer func() {
		glog.Infof("Listener at %s receieved interrupt", l.GetPath())
		l.Done()
		close(_shutdown)
		for len(processing) > 0 {
			delete(processing, <-done)
		}
	}()

	glog.V(1).Infof("Listener %s started; waiting for data", l.GetPath())
	for {
		nodes, event, err := conn.ChildrenW(l.GetPath())
		if err != nil {
			glog.Errorf("Could not watch for nodes at %s: %s", l.GetPath(), err)
			return
		}

		for _, node := range nodes {
			if _, ok := processing[node]; !ok {
				glog.V(1).Infof("Spawning a goroutine for %s", l.GetPath(node))
				processing[node] = struct{}{}
				go func(node string) {
					defer func() {
						glog.V(1).Infof("Goroutine at %s was shutdown", l.GetPath(node))
						done <- node
					}()
					l.Spawn(_shutdown, node)
				}(node)
			}
		}

		l.PostProcess(processing)

		select {
		case e := <-event:
			if e.Type == client.EventNodeDeleted {
				glog.V(1).Infof("Node %s has been removed; shutting down listener", l.GetPath())
				return
			}
			glog.V(4).Infof("Node %s receieved event %v", l.GetPath(), e)
		case node := <-done:
			glog.V(3).Infof("Cleaning up %s", l.GetPath(node))
			delete(processing, node)
		case <-shutdown:
			return
		}
	}
}

// Start starts a group of listeners that are governed by a master listener.
// When the master exits, it shuts down all of the child listeners and waits
// for all of the subprocesses to exit
func Start(shutdown <-chan interface{}, master Listener, listeners ...Listener) {
	var (
		wg        sync.WaitGroup
		_shutdown = make(chan interface{})
		done      = make(chan interface{})
		ready     = make(chan error, 1)
	)

	// Start up the master
	go func() {
		defer close(done)
		if master != nil {
			Listen(_shutdown, ready, master)
		} else {
			<-_shutdown
		}
	}()

	// Wait for the master to be ready and start the slave listeners
	select {
	case err := <-ready:
		if err != nil {
			break
		}

		for _, listener := range listeners {
			wg.Add(1)
			go func(l Listener) {
				defer wg.Done()
				Listen(_shutdown, make(chan error, 1), l)
			}(listener)
		}
	case <-done:
	case <-shutdown:
	}

	// Wait for the master to shutdown or shutdown signal
	select {
	case <-done:
	case <-shutdown:
	}

	// Wait for everything to stop
	close(_shutdown)
	wg.Wait()
}
