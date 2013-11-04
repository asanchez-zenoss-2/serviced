package dao

import "strconv"
import "time"
import "fmt"

type MinMax struct {
	Min int
	Max int
}

type ServiceTemplateWrapper struct {
  Id              string // Primary-key
	Name            string // Name of top level service
	Description     string // Description
	Data            string // JSON encoded template definition
	ApiVersion      int    // Version of the ServiceTemplate API this expects
	TemplateVersion int    // Version of the template
}

// An association between a host and a pool.
type PoolHost struct {
	HostId string
	PoolId string
  HostIp string
}

// A collection of computing resources with optional quotas.
type ResourcePool struct {
	Id          string // Unique identifier for resource pool, eg "default"
	ParentId    string // The pool id of the parent pool, if this pool is embeded in another pool. An empty string means it is not embeded.
	Priority    int    // relative priority of resource pools, used for CPU priority
	CoreLimit   int    // Number of cores on the host available to serviced
	MemoryLimit uint64 // A quota on the amount (bytes) of RAM in the pool, 0 = unlimited
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// A new ResourcePool
func NewResourcePool(id string) (*ResourcePool, error) {
  pool := &ResourcePool{}
	pool.Id = id
	return pool, nil
}

func (pool *ResourcePool) MakeSubpool(id string) *ResourcePool {
	subpool := *pool
	subpool.Id = id
	subpool.ParentId = pool.Id
	subpool.Priority = 0
	return &subpool
}

// A host that runs the control plane agent.
type Host struct {
	Id             string // Unique identifier, default to hostid
	Name           string // A label for the host, eg hostname, role
	PoolId         string // Pool that the Host belongs to
	IpAddr         string // The IP address the host can be reached at from a serviced master
	Cores          int    // Number of cores available to serviced
	Memory         uint64 // Amount of RAM (bytes) available to serviced
	PrivateNetwork string // The private network where containers run, eg 172.16.42.0/24
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Create a new host.
func NewHost() *Host {
	host := &Host{}
	return host
}

// Desired states of services.
const (
	SVC_RUN     = 1
	SVC_STOP    = 0
	SVN_RESTART = -1
)

// An exposed service endpoint
type ApplicationEndpoint struct {
	ServiceId     string
	ContainerPort uint16
	HostPort      uint16
	HostIp        string
	ContainerIp   string
	Protocol      string
}

// A Service that can run in serviced.
type Service struct {
	Id              string
	Name            string
	Context         string
	Startup         string
	Description     string
	Instances       int
	ImageId         string
	PoolId          string
	DesiredState    int
  Launch          string
	Endpoints       *[]ServiceEndpoint
	ParentServiceId string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// An endpoint that a Service exposes.
type ServiceEndpoint struct {
	Protocol    string
	PortNumber  uint16
	Application string
	Purpose     string
}

//export definition
type ServiceExport struct {
	Protocol    string //tcp or udp
	Application string //application type
	Internal    string //internal port number
	External    string //external port number
}

// An instantiation of a Service.
type ServiceState struct {
	Id              string
	ServiceId       string
	HostId          string
	DockerId        string
	PrivateIp       string
	Scheduled       time.Time
	Terminated      time.Time
	Started         time.Time
	PortMapping     map[string]map[string]string // protocol -> container port (internal) -> host port (external)
  Endpoints       []ServiceEndpoint
	HostIp          string
}

type ServiceDefinition struct {
	Name        string                 // Name of the defined service
	Command     string                 // Command which runs the service
	Description string                 // Description of the service
	ImageId     string                 // Docker image hosting the service
	Instances   MinMax                 // Constraints on the number of instances
	Launch      string                 // Must be "AUTO", the default, or "MANUAL"
  Context     map[string]interface{} // Context information for the service
	Endpoints   []ServiceEndpoint      // Comms endpoints used by the service
	Services    []ServiceDefinition    // Supporting subservices
}

type ServiceDeployment struct {
	Id         string    // Primary key
	TemplateId string    // id of template being deployed
	ServiceId  string    // id of service created by deployment
	DeployedAt time.Time // when the template was deployed
}

// A Service Template used for
type ServiceTemplate struct {
  Id          string              // Unique ID of this service template
	Name        string              // Name of service template
	Description string              // Meaningful description of service
	Services    []ServiceDefinition // Child services
}

// A request to deploy a service template
type ServiceTemplateDeploymentRequest struct {
	PoolId     string // Pool Id to deploy service into
	TemplateId string // Id of template to be deployed
}

// This is created by selecting from service_state and joining to service
type RunningService struct {
	Id              string
	ServiceId       string
	StartedAt       time.Time
	Name            string
	Startup         string
	Description     string
	Instances       int
	ImageId         string
	PoolId          string
	DesiredState    int
	ParentServiceId string
}

// Create a new Service.
func NewService() (s *Service, err error) {
	s = &Service{}
	s.Id, err = NewUuid()
	return s, err
}

// A new service instance (ServiceState)
func (s *Service) NewServiceState(hostId string) (serviceState *ServiceState, err error) {
	serviceState = &ServiceState{}
	serviceState.Id, err = NewUuid()
	if err == nil {
	  serviceState.ServiceId = s.Id
	  serviceState.HostId = hostId
	  serviceState.Scheduled = time.Now()
    serviceState.Endpoints = *s.Endpoints
	}
	return serviceState, err
}

// Does the service have endpoint imports
func (s *Service) HasImports() bool {
  if s.Endpoints == nil {
    return false
  }

  for _, ep := range *s.Endpoints {
    if ep.Purpose == "import" {
      return true
    }
  }
  return false
}

// Retrieve service endpoint imports
func (s *Service) GetServiceImports() (endpoints []ServiceEndpoint) {
  if s.Endpoints != nil {
    for _, ep := range *s.Endpoints {
      if ep.Purpose == "import" {
        endpoints = append( endpoints, ep)
      }
    }
  }
  return
}

// Retrieve service container port, 0 failure
func (ss *ServiceState) GetHostPort(protocol, application string, port uint16) uint16 {
  for _, ep := range ss.Endpoints {
    if ep.PortNumber == port && ep.Application == application && ep.Protocol == protocol && ep.Purpose == "export" {
      if protocol == "tcp" {
        protocol = "Tcp"
      } else if protocol == "udp" {
        protocol = "Udp"
      }

      portS := fmt.Sprintf( "%d", port)
      externalS := ss.PortMapping[protocol][portS]
      external, err := strconv.Atoi(externalS)
      if err == nil {
        return uint16( external)
      }
      break
    }
  }

  return 0
}