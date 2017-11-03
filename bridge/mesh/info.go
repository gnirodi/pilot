package mesh

import (
	"os"
	"sync"
	"time"
)

const (
	EnvVarNodeName          = "MY_NODE_NAME"
	EnvVarPodName           = "MY_POD_NAME"
	EnvVarPodNamespace      = "MY_POD_NAMESPACE"
	EnvVarPodIP             = "MY_POD_IP"
	EnvVarPodServiceAccount = "MY_POD_SERVICE_ACCOUNT"
	ServerStatus            = "SERVER_STATUS"
	StatusHeader            = "STATUS_HEADER"
	ServerStatusHeader      = "Istio Hybrid Multi-Zone Mesh Status"
	StartTime               = "START_TIME"
	UpdateTime              = "UPDATE_TIME"
)

type MeshInfo struct {
	labels           map[string]string
	agentWarnings    []string
	endpointsCreated int64
	endpointsUpdated int64
	endpointsDeleted int64
	endpointErrors   int64
	zones            map[string]ZoneDisplayInfo
	mu               sync.RWMutex
}

type ZoneDisplayInfo struct {
	ZoneName       string
	CountEndpoints int
}

func NewZoneDisplayInfo(z string) *ZoneDisplayInfo {
	zdi := ZoneDisplayInfo{z, 0}
	return &zdi
}

func NewMeshInfo() *MeshInfo {
	pi := MeshInfo{map[string]string{}, []string{}, 0, 0, 0, 0, map[string]ZoneDisplayInfo{}, sync.RWMutex{}}
	return &pi
}

func getTime() string {
	return time.Now().Format("2006-01-02 15:04:05 -07:00")
}

func (m *MeshInfo) BuildStatus(statusHeader string) {
	startTime := getTime()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.labels = map[string]string{
		StatusHeader:            statusHeader,
		EnvVarNodeName:          os.Getenv(EnvVarNodeName),
		EnvVarPodName:           os.Getenv(EnvVarPodName),
		EnvVarPodNamespace:      os.Getenv(EnvVarPodNamespace),
		EnvVarPodIP:             os.Getenv(EnvVarPodIP),
		EnvVarPodServiceAccount: os.Getenv(EnvVarPodServiceAccount),
		ServerStatus:            "Initializing",
		StartTime:               startTime,
		UpdateTime:              startTime,
	}
}

func (m *MeshInfo) SetStatus(currentRunInfo *MeshInfo) {
	m.mu.Lock()
	defer m.mu.Unlock()
	updateTime := getTime()
	m.labels[UpdateTime] = updateTime
	// These are free-form labels, do not nuke.
	// It's the responsibility of the caller to make sure that
	// currentRunInfo has just the right labels for status pages
	// to work.
	// i.e. new labels are added or old ones updated
	// nothing is deleted
	for k, v := range currentRunInfo.labels {
		m.labels[k] = v
	}
	m.agentWarnings = make([]string, len(currentRunInfo.agentWarnings))
	for i, v := range currentRunInfo.agentWarnings {
		m.agentWarnings[i] = v
	}
	m.endpointsCreated = m.endpointsCreated + currentRunInfo.endpointsCreated
	m.endpointsUpdated = m.endpointsUpdated + currentRunInfo.endpointsUpdated
	m.endpointsDeleted = m.endpointsDeleted + currentRunInfo.endpointsDeleted
	m.endpointErrors = m.endpointErrors + currentRunInfo.endpointErrors
	m.zones = map[string]ZoneDisplayInfo{}
	for i, v := range currentRunInfo.zones {
		m.zones[i] = v
	}
}

func (m *MeshInfo) GetValueOrNil(key string) *string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	val, ok := m.labels[key]
	if !ok {
		return nil
	}
	return &val
}

func (m *MeshInfo) GetStatusHeader() *string {
	return m.GetValueOrNil(StatusHeader)
}

func (m *MeshInfo) GetNodeName() *string {
	return m.GetValueOrNil(EnvVarNodeName)
}

func (m *MeshInfo) GetPodName() *string {
	return m.GetValueOrNil(EnvVarPodName)
}

func (m *MeshInfo) GetPodNamespace() *string {
	return m.GetValueOrNil(EnvVarPodNamespace)
}

func (m *MeshInfo) GetPodIP() *string {
	return m.GetValueOrNil(EnvVarPodIP)
}

func (m *MeshInfo) GetServerStatus() string {
	v := m.GetValueOrNil(ServerStatus)
	if v == nil {
		return ""
	} else {
		return *v
	}
}

func (m *MeshInfo) GetStartTime() *string {
	return m.GetValueOrNil(StartTime)
}

func (m *MeshInfo) GetUpdateTime() *string {
	return m.GetValueOrNil(UpdateTime)
}

func (m *MeshInfo) GetEndpointsCreated() int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.endpointsCreated
}

func (m *MeshInfo) GetEndpointsUpdated() int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.endpointsUpdated
}

func (m *MeshInfo) GetEndpointsDeleted() int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.endpointsDeleted
}

func (m *MeshInfo) GetEndpointErrors() int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.endpointErrors
}

func (m *MeshInfo) GetAgentWarnings() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.agentWarnings
}

func (m *MeshInfo) GetZoneDisplayInfo() map[string]ZoneDisplayInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.zones
}

// Setter methods are not gated by lock and are
// not intended to be thread safe. Use
// IncrementXxx or AddXxx in a single threaded manner
// and update a separate MeshInfo in a thread
// safe manner via SetStatus
func (m *MeshInfo) IncrementCountEndpointsCreated(c int64) {
	m.endpointsCreated = m.endpointsCreated + c
}

// Setter methods are not gated by lock and are
// not intended to be thread safe. Use
// IncrementXxx or AddXxx in a single threaded manner
// and update a separate MeshInfo in a thread
// safe manner via SetStatus
func (m *MeshInfo) IncrementCountEndpointsUpdated(c int64) {
	m.endpointsUpdated = m.endpointsUpdated + c
}

// Setter methods are not gated by lock and are
// not intended to be thread safe. Use
// IncrementXxx or AddXxx in a single threaded manner
// and update a separate MeshInfo in a thread
// safe manner via SetStatus
func (m *MeshInfo) IncrementCountEndpointsDeleted(c int64) {
	m.endpointsDeleted = m.endpointsDeleted + c
}

// Setter methods are not gated by lock and are
// not intended to be thread safe. Use
// IncrementXxx or AddXxx in a single threaded manner
// and update a separate MeshInfo in a thread
// safe manner via SetStatus
func (m *MeshInfo) IncrementCountEndpointErrors(c int64) {
	m.endpointErrors = m.endpointErrors + c
}

// AddAgentWarning is not gated by lock and is
// not intended to be thread safe. Use
// IncrementXxx or AddXxx in a single threaded manner
// and update a separate MeshInfo in a thread
// safe manner via SetStatus
func (m *MeshInfo) AddAgentWarning(w string) {
	m.agentWarnings = append(m.agentWarnings, w)
}

// AddDisplayZoneInfo is not gated by lock and is
// not intended to be thread safe. Use
// IncrementXxx or AddXxx in a single threaded manner
// and update a separate MeshInfo in a thread
// safe manner via SetStatus
func (m *MeshInfo) AddZoneDisplayInfo(z ZoneDisplayInfo) {
	m.zones[z.ZoneName] = z
}

func (m *MeshInfo) SetLabel(k, v string) {
	m.labels[k] = v
}
